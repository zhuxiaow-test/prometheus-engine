// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lease

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/go-kit/kit/log"
	"github.com/pkg/errors"
	"google.golang.org/api/option"
	monitoredres_pb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoring_pb "google.golang.org/genproto/googleapis/monitoring/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	empty_pb "google.golang.org/protobuf/types/known/emptypb"
)

type testMetricService struct {
	monitoring_pb.MetricServiceServer // Inherit all interface methods

	latency        time.Duration
	frequencyLimit time.Duration
	// TODO(freinartz): add error probability

	mtx          sync.Mutex
	lastInterval *monitoring_pb.TimeInterval
}

func (srv *testMetricService) CreateTimeSeries(ctx context.Context, req *monitoring_pb.CreateTimeSeriesRequest) (*empty_pb.Empty, error) {
	select {
	case <-time.After(srv.latency):
	case <-ctx.Done():
		return nil, status.Error(codes.Canceled, "canceled")
	}
	srv.mtx.Lock()
	defer srv.mtx.Unlock()

	// Assume well-formed requests and just validate timestamps according to the logic
	// of the real API.
	// We expect all clients to write against the same lease for the test.
	iv := req.TimeSeries[0].Points[0].Interval
	// First write always succeeds.
	if srv.lastInterval != nil {
		// The end timestamp must not go into the past and not be too shortly after
		// the last one.
		if iv.EndTime.AsTime().Before(srv.lastInterval.EndTime.AsTime()) {
			return nil, status.Error(codes.InvalidArgument, "end time before last end time")
		}
		if iv.EndTime.AsTime().Before(srv.lastInterval.EndTime.AsTime().Add(srv.frequencyLimit)) {
			return nil, status.Error(codes.InvalidArgument, "sample frequency too high")
		}
		// Start time must not be before or at the last start time.
		if iv.StartTime.AsTime().Before(srv.lastInterval.StartTime.AsTime()) {
			return nil, status.Error(codes.InvalidArgument, "start time before last start time")
		}
	}
	srv.lastInterval = iv

	return &empty_pb.Empty{}, nil
}

type interval struct {
	baseTime time.Time
	replica  string

	start, end time.Time
}

func (iv interval) String() string {
	return fmt.Sprintf("%s[%s, %s]", iv.replica, iv.start.Sub(iv.baseTime), iv.end.Sub(iv.baseTime))
}

type replicaIntervals struct {
	leaseOpts *Options
	baseTime  time.Time

	mtx sync.Mutex
	m   map[string][]interval
}

func newReplicaIntervals(leaseOpts *Options, baseTime time.Time) *replicaIntervals {
	return &replicaIntervals{
		leaseOpts: leaseOpts,
		baseTime:  baseTime,
		m:         map[string][]interval{},
	}
}

func (ri *replicaIntervals) add(replica string, start, end time.Time) {
	ri.mtx.Lock()
	defer ri.mtx.Unlock()

	intervals := ri.m[replica]
	// Do a replace if an interval is merely extended.
	if last := len(intervals) - 1; last >= 0 && intervals[last].start.Equal(start) {
		intervals = intervals[:last]
	}
	ri.m[replica] = append(intervals, interval{
		replica:  replica,
		baseTime: ri.baseTime,
		start:    start,
		end:      end,
	})
}

func (ri replicaIntervals) intervals() []interval {
	ri.mtx.Lock()
	var intervals []interval
	for _, iv := range ri.m {
		intervals = append(intervals, iv...)
	}
	ri.mtx.Unlock()

	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].start.Before(intervals[j].start)
	})
	return intervals
}

func (ri *replicaIntervals) validate() error {
	// Intervals must never overlap but not have large gaps either.
	// Start with a dummy interval at the front to simplify comparison.
	iv1 := interval{
		replica:  "dummy",
		baseTime: ri.baseTime,
		// A very quickly acquired lease may reach up to 1ms into the past with an
		// empty initial state, so account for that.
		start: ri.baseTime.Add(-time.Millisecond),
		end:   ri.baseTime.Add(-time.Millisecond),
	}

	for _, iv2 := range ri.intervals() {
		// Intervals are closed and thus must strictly not overlap.
		if !iv1.end.Before(iv2.start) && !iv2.end.Before(iv1.start) {
			return errors.Errorf("intervals for replica %s and %s overlap", iv1, iv2)
		}
		// Largest possible gap is the 1 extension period + 1 update period. At least
		// while we don't have randomized transient errors.
		if gap := iv2.start.Sub(iv1.end); gap > ri.leaseOpts.ExtensionPeriod+ri.leaseOpts.UpdatePeriod {
			return errors.Errorf("gap of %s between intervals %s and %s too large", gap, iv1, iv2)
		}
		iv1 = iv2
	}
	return nil
}

func testLease(t *testing.T, duration time.Duration, leaseOpts *Options, replicaCount int, replicaLifetime time.Duration) []interval {
	srv := grpc.NewServer()
	listener := bufconn.Listen(1e6)

	metricServer := &testMetricService{
		latency:        leaseOpts.RetryPeriod / 10,
		frequencyLimit: leaseOpts.UpdatePeriod,
	}
	monitoring_pb.RegisterMetricServiceServer(srv, metricServer)

	go func() { srv.Serve(listener) }()
	defer srv.Stop()

	// Run multiple leases simultaniously and wait for a condition violation for the given time.
	// If there is a leader most of the time and never more than one leader, everything is fine.
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	bufDialer := func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}
	client, err := monitoring.NewMetricClient(ctx,
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithInsecure()),
		option.WithGRPCDialOption(grpc.WithContextDialer(bufDialer)),
	)
	if err != nil {
		t.Fatalf("creating metric client failed: %s", err)
	}

	var (
		resource  = &monitoredres_pb.MonitoredResource{Type: "prometheus_target"}
		intervals = newReplicaIntervals(leaseOpts, time.Now())
		// Control number of concurrent replicas via channel.
		replicaCh = make(chan struct{}, 100)
		errCh     = make(chan error)
	)
	hook := func(name string, start, end time.Time, valid bool) {
		if valid {
			intervals.add(name, start, end)
		}
	}

Loop:
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			break Loop
		case replicaCh <- struct{}{}:
		}
		// Keep producing new replica IDs. In practice IDs would be recycled but
		// this makes it easier to debug history.
		name := fmt.Sprintf("replica-%d", i)

		logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stdout))
		logger = log.With(logger, "ts", log.DefaultTimestampUTC, "replica", name)

		lease, err := New(nil, "test-project", client, resource, "replica-key", leaseOpts)
		if err != nil {
			t.Fatalf("Failed to create lease: %s", err)
		}
		lease.Register(func(start, end time.Time, ok bool) {
			hook(name, start, end, ok)
		})
		// Make replicas die after a random amount of time unless a fixed lifetime is given.
		if replicaLifetime == 0 {
			replicaLifetime = time.Duration(rand.Intn(int(50 * leaseOpts.ExtensionPeriod)))
		}
		t.Logf("starting %s with lifetime %s", name, replicaLifetime)

		ctx, cancel := context.WithTimeout(ctx, replicaLifetime)
		go func() {
			if err := lease.Run(ctx); err != nil {
				errCh <- err
			}
			cancel()
			// Free replica spot on exit.
			<-replicaCh
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		t.Fatal(err)
	}
	t.Log(intervals.intervals())

	if err := intervals.validate(); err != nil {
		t.Fatal(err)
	}
	return intervals.intervals()
}

func TestLease(t *testing.T) {
	opts := &Options{
		UpdatePeriod:    10 * time.Millisecond,
		RetryPeriod:     1 * time.Millisecond,
		ExtensionPeriod: 30 * time.Millisecond,
	}

	t.Run("one-lease-interval", func(t *testing.T) {
		// This test ensures that the lease is not flip-flopping between participants if they
		// remain alive.
		t.Parallel()
		intervals := testLease(t, 5 * time.Second, opts, 10, time.Hour)

		if len(intervals) != 1 {
			t.Fatalf("expected exactly one interval with long-running replicas but got %d", len(intervals))
		}
	})
	t.Run("many-intermittent-replicas", func(t *testing.T) {
		// This test attempts to trigger a bad condition by having a lease change often across
		// a high number of replicas.
		t.Parallel()
		testLease(t, 10 * time.Second, opts, 100, 0)
	})
}