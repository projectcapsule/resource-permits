// Copyright 2020-2026 Project Capsule Authors
// SPDX-License-Identifier: Apache-2.0

package resourcepermit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	permitsv1beta2 "github.com/projectcapsule/resource-permits/api/v1beta2"
	"github.com/projectcapsule/resource-permits/internal/webhook/test"
)

func BenchmarkPermitDeleteAuthorization(b *testing.B) {
	future := metav1.NewTime(time.Now().Add(time.Hour))
	for _, size := range []int{1, 100} {
		for _, tt := range []struct {
			name                 string
			phase                permitsv1beta2.ResourcePermitPhase
			keepUntil            *metav1.Time
			terminating, allowed bool
		}{
			{name: "active", phase: permitsv1beta2.ResourcePermitPhaseActive},
			{name: "retained", phase: permitsv1beta2.ResourcePermitPhaseExpired, keepUntil: &future},
			{name: "expired", phase: permitsv1beta2.ResourcePermitPhaseExpired, allowed: true},
			{name: "terminating", phase: permitsv1beta2.ResourcePermitPhaseActive, terminating: true, allowed: true},
		} {
			b.Run(fmt.Sprintf("namespaces=%d/%s", size, tt.name), func(b *testing.B) {
				scheme := runtime.NewScheme()
				if err := corev1.AddToScheme(scheme); err != nil {
					b.Fatal(err)
				}
				objects := make([]client.Object, size)
				for i := range objects {
					objects[i] = &corev1.Namespace{Name: fmt.Sprintf("team-%d", i)}
				}
				if tt.terminating {
					objects[0].(*corev1.Namespace).Status.Phase = corev1.NamespaceTerminating
				}
				reads := 0
				reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						reads++
						return cl.Get(ctx, key, obj, opts...)
					},
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						b.Fatal("deletion must not list namespaces or Capsule resources")
						return nil
					},
				}).Build()
				decoder := &test.Decoder[*permitsv1beta2.ResourcePermit]{OldObject: &permitsv1beta2.ResourcePermit{Namespace: "team-0", Status: permitsv1beta2.ResourcePermitStatus{Phase: tt.phase, KeepUntil: tt.keepUntil}}}
				call := ResourcePermitValidationHandler(logr.Discard()).OnDelete(nil, reader, decoder, nil)
				req := admission.Request{Namespace: "team-0", UserInfo: authenticationv1.UserInfo{Username: "operator", Groups: []string{"system:masters"}}}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					response := call(context.Background(), req)
					if tt.allowed && response != nil {
						b.Fatal("valid deletion denied")
					}
					if !tt.allowed && (response == nil || response.Allowed) {
						b.Fatal("protected permit deletion allowed")
					}
				}
				b.StopTimer()
				if reads != b.N {
					b.Fatalf("namespace reads = %d, want %d", reads, b.N)
				}
				b.ReportMetric(float64(reads)/float64(b.N), "namespace-gets/op")
			})
		}
	}
}
