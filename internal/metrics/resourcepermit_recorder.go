// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	crtlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
)

type ResourcePermitsRecorder struct {
	phaseGauge *prometheus.GaugeVec
}

func MustMakeResourcePermitsRecorder() *ResourcePermitsRecorder {
	metricsRecorder := NewResourcePermitsRecorder()
	crtlmetrics.Registry.MustRegister(metricsRecorder.Collectors()...)

	return metricsRecorder
}

func NewResourcePermitsRecorder() *ResourcePermitsRecorder {
	return &ResourcePermitsRecorder{
		phaseGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricsPrefix,
				Name:      "resourcepermit_phase",
				Help:      "The current phase of the ResourcePermit.",
			},
			[]string{"name", "target_namespace", "template", "template_type", "requestor", "status"},
		),
	}
}

func (r *ResourcePermitsRecorder) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		r.phaseGauge,
	}
}

// RecordResourcePermitPhase records the current phase of the ResourcePermit.
func (r *ResourcePermitsRecorder) RecordResourcePermitPhase(rp *permitsv1beta2.ResourcePermit) {
	if r == nil || r.phaseGauge == nil || rp == nil {
		return
	}

	tmplType := templateType(rp)

	// Remove previous status series for this request.
	r.phaseGauge.DeletePartialMatch(map[string]string{
		"name":             rp.GetName(),
		"target_namespace": rp.GetNamespace(),
		"template":         rp.Spec.Template.Name,
		"template_type":    tmplType,
		"requestor":        rp.Spec.Requestor.Name,
	})

	if rp.Status.Phase == "" {
		return
	}

	r.phaseGauge.WithLabelValues(rp.GetName(), rp.GetNamespace(), rp.Spec.Template.Name, tmplType, rp.Spec.Requestor.Name, string(rp.Status.Phase)).Set(1)
}

// DeleteResourcePermitMetrics deletes all metrics series for the given ResourcePermit.
func (r *ResourcePermitsRecorder) DeleteResourcePermitMetrics(rp *permitsv1beta2.ResourcePermit) {
	if r == nil || r.phaseGauge == nil || rp == nil {
		return
	}

	r.phaseGauge.DeletePartialMatch(map[string]string{
		"name":             rp.GetName(),
		"target_namespace": rp.GetNamespace(),
		"template":         rp.Spec.Template.Name,
		"template_type":    templateType(rp),
		"requestor":        rp.Spec.Requestor.Name,
	})
}

func templateType(rp *permitsv1beta2.ResourcePermit) string {
	switch rp.Spec.Template.Kind {
	case "GlobalResourcePermitTemplate":
		return "global"
	case "ResourcePermitTemplate":
		return "namespaced"
	}

	return ""
}
