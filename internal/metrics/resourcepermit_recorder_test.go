// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	"github.com/projectcapsule/resource-permits/pkg/api/resourcepermit"
)

func TestResourcePermitPhaseMetrics(t *testing.T) {
	t.Parallel()

	recorder := NewResourcePermitsRecorder()
	registry := prometheus.NewRegistry()
	registry.MustRegister(recorder.Collectors()...)

	// Record a namespaced-template permit.
	rp := &permitsv1beta2.ResourcePermit{
		Name:      "permit-a",
		Namespace: "team-a",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "alice"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseRequested,
		},
	}

	recorder.RecordResourcePermitPhase(rp)
	assertGauge(t, recorder.phaseGauge, 1, "permit-a", "team-a", "tmpl", "namespaced", "alice", string(permitsv1beta2.ResourcePermitPhaseRequested))

	// Verify the metric is present in the registry.
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Equal(t, "resource_permits_resourcepermit_phase", families[0].GetName())
	require.Len(t, families[0].Metric, 1)

	// Updating the phase replaces the previous status label.
	rp.Status.Phase = permitsv1beta2.ResourcePermitPhaseApproved
	recorder.RecordResourcePermitPhase(rp)
	assertGauge(t, recorder.phaseGauge, 1, "permit-a", "team-a", "tmpl", "namespaced", "alice", string(permitsv1beta2.ResourcePermitPhaseApproved))

	// Only the updated phase is present.
	families, err = registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Len(t, families[0].Metric, 1)
}

func TestResourcePermitPhaseMetrics_GlobalTemplate(t *testing.T) {
	t.Parallel()

	recorder := NewResourcePermitsRecorder()

	rp := &permitsv1beta2.ResourcePermit{
		Name:      "permit-g",
		Namespace: "team-b",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "GlobalResourcePermitTemplate",
				Name: "global-tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "bob"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseActive,
		},
	}

	recorder.RecordResourcePermitPhase(rp)
	assertGauge(t, recorder.phaseGauge, 1, "permit-g", "team-b", "global-tmpl", "global", "bob", string(permitsv1beta2.ResourcePermitPhaseActive))
}

func TestResourcePermitPhaseMetrics_EmptyPhase(t *testing.T) {
	t.Parallel()

	recorder := NewResourcePermitsRecorder()

	rp := &permitsv1beta2.ResourcePermit{
		Name:      "permit-empty",
		Namespace: "team-a",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "alice"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: "",
		},
	}

	recorder.RecordResourcePermitPhase(rp)
	require.Zero(t, metricCount(recorder.phaseGauge))
}

func TestResourcePermitPhaseMetrics_NilGuards(t *testing.T) {
	t.Parallel()

	// Nil recorder must not panic.
	var nilRecorder *ResourcePermitsRecorder
	rp := &permitsv1beta2.ResourcePermit{
		Name:      "permit-nil",
		Namespace: "team-a",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "alice"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseRequested,
		},
	}

	nilRecorder.RecordResourcePermitPhase(rp)
	nilRecorder.DeleteResourcePermitMetrics(rp)
}

func TestResourcePermitPhaseMetrics_Delete(t *testing.T) {
	t.Parallel()

	recorder := NewResourcePermitsRecorder()

	rp := &permitsv1beta2.ResourcePermit{
		Name:      "permit-del",
		Namespace: "team-a",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "alice"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseRequested,
		},
	}

	recorder.RecordResourcePermitPhase(rp)
	require.Equal(t, 1, metricCount(recorder.phaseGauge))

	recorder.DeleteResourcePermitMetrics(rp)
	require.Zero(t, metricCount(recorder.phaseGauge))
}

func TestResourcePermitPhaseMetrics_MultiNamespace(t *testing.T) {
	t.Parallel()

	recorder := NewResourcePermitsRecorder()

	rpA := &permitsv1beta2.ResourcePermit{
		Name:      "permit-shared",
		Namespace: "team-a",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "alice"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseRequested,
		},
	}

	rpB := &permitsv1beta2.ResourcePermit{
		Name:      "permit-shared",
		Namespace: "team-b",
		Spec: permitsv1beta2.ResourcePermitSpec{
			Template: permitsv1beta2.ResourcePermitTemplateReference{
				Kind: "ResourcePermitTemplate",
				Name: "tmpl",
			},
			Requestor: resourcepermit.AccessEntity{Name: "bob"},
		},
		Status: permitsv1beta2.ResourcePermitStatus{
			Phase: permitsv1beta2.ResourcePermitPhaseApproved,
		},
	}

	recorder.RecordResourcePermitPhase(rpA)
	recorder.RecordResourcePermitPhase(rpB)
	require.Equal(t, 2, metricCount(recorder.phaseGauge))

	// Deleting one namespace's permit must not affect the other.
	recorder.DeleteResourcePermitMetrics(rpA)
	require.Equal(t, 1, metricCount(recorder.phaseGauge))
	assertGauge(t, recorder.phaseGauge, 1, "permit-shared", "team-b", "tmpl", "namespaced", "bob", string(permitsv1beta2.ResourcePermitPhaseApproved))
}

func TestTemplateType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind string
		want string
	}{
		{kind: "GlobalResourcePermitTemplate", want: "global"},
		{kind: "ResourcePermitTemplate", want: "namespaced"},
		{kind: "Unknown", want: ""},
		{kind: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			rp := &permitsv1beta2.ResourcePermit{
				Spec: permitsv1beta2.ResourcePermitSpec{
					Template: permitsv1beta2.ResourcePermitTemplateReference{
						Kind: tc.kind,
					},
				},
			}
			require.Equal(t, tc.want, templateType(rp))
		})
	}
}
