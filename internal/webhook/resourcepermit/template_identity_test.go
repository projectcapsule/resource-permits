// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package resourcepermit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/projectcapsule/capsule/pkg/runtime/handlers"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
)

func templateIdentityAdmission(tb testing.TB, global bool, operation admissionv1.Operation, identity any) (handlers.Func, admission.Request) {
	tb.Helper()
	scheme := runtime.NewScheme()
	if err := permitsv1beta2.AddToScheme(scheme); err != nil {
		tb.Fatal(err)
	}
	kind := "ResourcePermitTemplate"
	handler := ResourcePermitTemplateValidationHandler(logr.Discard())
	if global {
		kind = "GlobalResourcePermitTemplate"
		handler = GlobalResourcePermitTemplateValidationHandler(logr.Discard())
	}
	object := map[string]any{
		"apiVersion": permitsv1beta2.GroupVersion.String(), "kind": kind,
		"metadata": map[string]any{"name": "example", "namespace": "team-a"},
		"spec": map[string]any{
			"impersonation": identity,
			"resources":     []any{map[string]any{"template": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n"}},
		},
	}
	raw, err := json.Marshal(object)
	if err != nil {
		tb.Fatal(err)
	}
	req := admission.Request{Operation: operation, Object: runtime.RawExtension{Raw: raw}}
	decoder := admission.NewDecoder(scheme)
	// Nil clients ensure validation and skip paths cannot perform API reads.
	switch operation {
	case admissionv1.Update:
		return handler.OnUpdate(nil, nil, decoder, nil), req
	case admissionv1.Delete:
		return handler.OnDelete(nil, nil, decoder, nil), req
	default:
		return handler.OnCreate(nil, nil, decoder, nil), req
	}
}

func TestTemplateAdmissionRequiresServiceAccount(t *testing.T) {
	t.Parallel()
	for _, global := range []bool{false, true} {
		for _, operation := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
			for _, tt := range []struct {
				name     string
				identity any
				valid    bool
			}{
				{name: "missing"},
				{name: "empty", identity: map[string]string{}},
				{name: "name only", identity: map[string]string{"name": "runner"}, valid: !global},
				{name: "namespace only", identity: map[string]string{"namespace": "operations"}},
				{name: "explicit", identity: map[string]string{"name": "runner", "namespace": "operations"}, valid: true},
			} {
				t.Run(fmt.Sprintf("global=%t/%s/%s", global, operation, tt.name), func(t *testing.T) {
					call, req := templateIdentityAdmission(t, global, operation, tt.identity)
					response := call(t.Context(), req)
					if tt.valid || operation == admissionv1.Delete {
						require.Nil(t, response)
					} else {
						require.NotNil(t, response)
						require.False(t, response.Allowed)
						require.EqualValues(t, http.StatusForbidden, response.Result.Code)
						require.Contains(t, response.Result.Message, "spec.impersonation")
					}
				})
			}
		}
	}
}

func BenchmarkTemplateIdentityAdmission(b *testing.B) {
	for _, global := range []bool{false, true} {
		for _, operation := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
			for _, valid := range []bool{false, true} {
				b.Run(fmt.Sprintf("global=%t/%s/valid=%t", global, operation, valid), func(b *testing.B) {
					var identity any
					if valid {
						identity = map[string]string{"name": "runner", "namespace": "operations"}
					}
					call, req := templateIdentityAdmission(b, global, operation, identity)
					b.ReportAllocs()
					for b.Loop() {
						response := call(b.Context(), req)
						if valid || operation == admissionv1.Delete {
							if response != nil {
								b.Fatal("valid template or deletion denied")
							}
						} else if response == nil || response.Allowed {
							b.Fatal("missing executor allowed")
						}
					}
				})
			}
		}
	}
}
