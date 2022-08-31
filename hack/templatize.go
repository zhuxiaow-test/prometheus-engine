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

// This file gets run to put concrete values into our deployment manifests where
// there is a value of {{.<Var>}}. To add a new template var, put it in the
// struct below and use it in templates like {{.<VarName>}}
package main

import (
	"flag"
	"os"
	"path/filepath"
	"text/template"
)

type data struct {
	OperatorImage          string
	PrometheusImage        string
	OperatorUserAgent      string
	RuleEvaluatorUserAgent string
	ConfigReloaderImage    string
	RuleEvaluatorImage     string
	GMPSystemNamespace     string
	GMPPublicNamespace     string
}

// default to the most recently released if not supplied.
const (
	defaultOperatorImage          = "gke.gcr.io/prometheus-engine/operator:v0.4.3-gke.0"
	defaultPrometheusImage        = "gke.gcr.io/prometheus-engine/prometheus:v2.35.0-gmp.2-gke.0"
	defaultOperatorUserAgent      = "prometheus/2.35.0-gmp.2 (mode:kubectl)"
	defaultRuleEvaluatorUserAgent = "rule-evaluator/0.4.1 (mode:kubectl)"
	defaultConfigReloaderImage    = "gke.gcr.io/prometheus-engine/config-reloader:v0.4.1-gke.0"
	defaultRuleEvaluatorImage     = "gke.gcr.io/prometheus-engine/rule-evaluator:v0.4.3-gke.0"
	defaultGMPSystemNamespace     = "gmp-system"
	defaultGMPPublicNamespace     = "gmp-public"
)

func main() {
	var (
		operatorImage          = flag.String("operator-image", defaultOperatorImage, "operator image to use")
		prometheusImage        = flag.String("prometheus-image", defaultPrometheusImage, "prometheus image to use")
		operatorUserAgent      = flag.String("operator-user-agent", defaultOperatorUserAgent, "operator user agent")
		ruleEvaluatorUserAgent = flag.String("rule-evaluator-user-agent", defaultRuleEvaluatorUserAgent, " rule evaluatoruser agent")
		configReloaderImage    = flag.String("config-reloader-image", defaultConfigReloaderImage, "config-reloader image to use")
		ruleEvaluatorImage     = flag.String("rule-evaluator-image", defaultRuleEvaluatorImage, "rule-evaluator image to use")
		gmpSystemNameSpace     = flag.String("gmp-system-name-space", defaultGMPSystemNamespace, "gmp-system namespace")
		gmpPublicNameSpace     = flag.String("gmp-public-name-space", defaultGMPPublicNamespace, "gmp-public namespace")
		input                  = flag.String("input", "", "the input files to process (can be a glob pattern for multiple)")
		outputDir              = flag.String("output-dir", "./out", "the directory to write the output files to")
	)
	flag.Parse()

	args := data{
		OperatorImage:          *operatorImage,
		PrometheusImage:        *prometheusImage,
		OperatorUserAgent:      *operatorUserAgent,
		RuleEvaluatorUserAgent: *ruleEvaluatorUserAgent,
		ConfigReloaderImage:    *configReloaderImage,
		RuleEvaluatorImage:     *ruleEvaluatorImage,
		GMPSystemNamespace:     *gmpSystemNameSpace,
		GMPPublicNamespace:     *gmpPublicNameSpace,
	}

	inFiles, err := filepath.Glob(*input)
	if err != nil {
		panic(err)
	}

	for _, inFile := range inFiles {
		t, err := template.ParseFiles(inFile)
		if err != nil {
			panic(err)
		}
		err = os.MkdirAll(*outputDir, 0770)
		if err != nil {
			panic(err)
		}
		out := *outputDir + string(filepath.Separator) + filepath.Base(inFile)
		f, err := os.Create(out)
		if err != nil {
			panic(err)
		}
		err = t.Execute(f, args)
		if err != nil {
			panic(err)
		}
		f.Close()
	}

}
