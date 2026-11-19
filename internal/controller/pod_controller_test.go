/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: labels},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
		},
	}
}

var _ = Describe("Pod Controller", func() {
	var reconciler *PodReconciler

	BeforeEach(func() {
		reconciler = &PodReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	reconcilePod := func(name string) *corev1.Pod {
		key := types.NamespacedName{Name: name, Namespace: "default"}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		got := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		return got
	}

	It("adds the label to a pod with no labels", func() {
		pod := newPod("no-labels", nil)
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, pod)

		got := reconcilePod(pod.Name)
		Expect(got.Labels).To(HaveKeyWithValue(LabelKey, LabelValue))
	})

	It("adds the label and keeps existing labels", func() {
		pod := newPod("other-labels", map[string]string{"app": "web"})
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, pod)

		got := reconcilePod(pod.Name)
		Expect(got.Labels).To(HaveKeyWithValue(LabelKey, LabelValue))
		Expect(got.Labels).To(HaveKeyWithValue("app", "web"))
	})

	It("leaves a pod alone when the label key already exists", func() {
		pod := newPod("already-labelled", map[string]string{LabelKey: "false"})
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, pod)

		got := reconcilePod(pod.Name)
		Expect(got.Labels).To(HaveKeyWithValue(LabelKey, "false"))
		Expect(got.ResourceVersion).To(Equal(pod.ResourceVersion))
	})

	It("ignores pods that no longer exist", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
