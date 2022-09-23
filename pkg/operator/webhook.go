// Copyright 2022 Google LLC
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

package operator

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	monitoringv1 "github.com/GoogleCloudPlatform/prometheus-engine/pkg/operator/apis/monitoring/v1"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	arv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/cert"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type WebhookManager struct {
	logger logr.Logger
	opts   Options
	client client.Client
}

// Delete old ValidatingWebhookConfiguration that was installed directly by the operator in previous
// versions.
func (m *WebhookManager) Cleanup(ctx context.Context) error {
	err := m.client.Delete(ctx, &arv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "gmp-operator"},
	})
	if err != nil && !apierrors.IsNotFound(err) {
		m.logger.Error(err, "deleting legacy ValidatingWebhookConfiguration")
		return err
	}
	return nil
}

// ensureCerts writes the cert/key files to the specified directory.
// If cert/key are not avalilable, generate them.
func (m *WebhookManager) ensureCerts(ctx context.Context, dir string) ([]byte, error) {
	var (
		crt, key, caData []byte
		err              error
	)
	if m.opts.TLSKey != "" && m.opts.TLSCert != "" {
		crt, err = base64.StdEncoding.DecodeString(m.opts.TLSCert)
		if err != nil {
			return nil, errors.Wrap(err, "decoding TLS certificate")
		}
		key, err = base64.StdEncoding.DecodeString(m.opts.TLSKey)
		if err != nil {
			return nil, errors.Wrap(err, "decoding TLS key")
		}
		if m.opts.CACert != "" {
			caData, err = base64.StdEncoding.DecodeString(m.opts.CACert)
			if err != nil {
				return nil, errors.Wrap(err, "decoding certificate authority")
			}
		}
	} else if m.opts.TLSKey == "" && m.opts.TLSCert == "" && m.opts.CACert == "" {
		// Generate a self-signed pair if none was explicitly provided. It will be valid
		// for 1 year.
		// TODO(freinartz): re-generate at runtime and update the ValidatingWebhookConfiguration
		// at runtime whenever the files change.
		fqdn := fmt.Sprintf("%s.%s.svc", NameOperator, m.opts.OperatorNamespace)

		crt, key, err = cert.GenerateSelfSignedCertKey(fqdn, nil, nil)
		if err != nil {
			return nil, errors.Wrap(err, "generate self-signed TLS key pair")
		}
		// Use crt as the ca in the the self-sign case.
		caData = crt
	} else {
		return nil, errors.Errorf("Flags key-base64 and cert-base64 must both be set.")
	}
	// Create cert/key files.
	if err := ioutil.WriteFile(filepath.Join(dir, "tls.crt"), crt, 0666); err != nil {
		return nil, errors.Wrap(err, "create cert file")
	}
	if err := ioutil.WriteFile(filepath.Join(dir, "tls.key"), key, 0666); err != nil {
		return nil, errors.Wrap(err, "create key file")
	}
	return caData, nil
}

// setupAdmissionWebhooks configures validating webhooks for the operator-managed
// custom resources and registers handlers with the webhook server.
func (m *WebhookManager) Run(ctx context.Context, s *webhook.Server) error {
	// Write provided cert files.
	caBundle, err := m.ensureCerts(ctx, s.CertDir)
	if err != nil {
		return err
	}

	// Keep setting the caBundle in the expected webhook configurations.
	go func() {
		// Only inject if we've an explicit CA bundle ourselves. Otherwise the webhook configs
		// may already have been created with one.
		if len(caBundle) == 0 {
			return
		}
		// Initial sleep for the client to initialize before our first calls.
		// Ideally we could explicitly wait for it.
		time.Sleep(5 * time.Second)

		for {
			if err := m.setValidatingWebhookCABundle(ctx, caBundle); err != nil {
				m.logger.Error(err, "Setting CA bundle for ValidatingWebhookConfiguration failed")
			}
			if err := m.setMutatingWebhookCABundle(ctx, caBundle); err != nil {
				m.logger.Error(err, "Setting CA bundle for MutatingWebhookConfiguration failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()

	// Validating webhooks.
	s.Register(
		validatePath(monitoringv1.PodMonitoringResource()),
		admission.ValidatingWebhookFor(&monitoringv1.PodMonitoring{}),
	)
	s.Register(
		validatePath(monitoringv1.ClusterPodMonitoringResource()),
		admission.ValidatingWebhookFor(&monitoringv1.ClusterPodMonitoring{}),
	)
	s.Register(
		validatePath(monitoringv1.OperatorConfigResource()),
		admission.WithCustomValidator(&monitoringv1.OperatorConfig{}, &operatorConfigValidator{
			namespace: m.opts.PublicNamespace,
		}),
	)
	s.Register(
		validatePath(monitoringv1.RulesResource()),
		admission.WithCustomValidator(&monitoringv1.Rules{}, &rulesValidator{
			opts: m.opts,
		}),
	)
	s.Register(
		validatePath(monitoringv1.ClusterRulesResource()),
		admission.WithCustomValidator(&monitoringv1.ClusterRules{}, &clusterRulesValidator{
			opts: m.opts,
		}),
	)
	s.Register(
		validatePath(monitoringv1.GlobalRulesResource()),
		admission.WithCustomValidator(&monitoringv1.GlobalRules{}, &globalRulesValidator{}),
	)
	// Defaulting webhooks.
	s.Register(
		defaultPath(monitoringv1.PodMonitoringResource()),
		admission.WithCustomDefaulter(&monitoringv1.PodMonitoring{}, &podMonitoringDefaulter{}),
	)
	s.Register(
		defaultPath(monitoringv1.ClusterPodMonitoringResource()),
		admission.WithCustomDefaulter(&monitoringv1.ClusterPodMonitoring{}, &clusterPodMonitoringDefaulter{}),
	)
	return nil
}

func validatePath(gvr metav1.GroupVersionResource) string {
	return fmt.Sprintf("/validate/%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)
}

func defaultPath(gvr metav1.GroupVersionResource) string {
	return fmt.Sprintf("/default/%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)
}

func (m *WebhookManager) webhookConfigName() string {
	return fmt.Sprintf("%s.%s.monitoring.googleapis.com", NameOperator, m.opts.OperatorNamespace)
}

func (m *WebhookManager) setValidatingWebhookCABundle(ctx context.Context, caBundle []byte) error {
	var vwc arv1.ValidatingWebhookConfiguration
	err := m.client.Get(ctx, client.ObjectKey{Name: m.webhookConfigName()}, &vwc)
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	for i := range vwc.Webhooks {
		vwc.Webhooks[i].ClientConfig.CABundle = caBundle
	}
	return m.client.Update(ctx, &vwc)
}

func (m *WebhookManager) setMutatingWebhookCABundle(ctx context.Context, caBundle []byte) error {
	var mwc arv1.MutatingWebhookConfiguration
	err := m.client.Get(ctx, client.ObjectKey{Name: m.webhookConfigName()}, &mwc)
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	for i := range mwc.Webhooks {
		mwc.Webhooks[i].ClientConfig.CABundle = caBundle
	}
	return m.client.Update(ctx, &mwc)
}
