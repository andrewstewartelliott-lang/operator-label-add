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
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// LabelKey is the label the operator ensures is present on every new Pod.
	LabelKey = "app.aelliott.io/label"
	// LabelValue is the value written when the label is absent.
	LabelValue = "true"
)

// PodReconciler adds LabelKey=LabelValue to newly-created Pods that lack it.
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Least privilege: the operator only needs to read pods and patch their metadata.
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;patch

// Reconcile is called for every Pod creation event (see SetupWithManager).
// If the Pod does not carry the label key, the label is added via a merge
// patch and a log line is written.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		// The Pod may have been deleted between the event and this reconcile.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Pods that are already being deleted are left alone.
	if !pod.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// "If it does not exist": only the presence of the key matters. A pod
	// that already has the key (with any value) is not modified.
	if _, ok := pod.Labels[LabelKey]; ok {
		return ctrl.Result{}, nil
	}

	original := pod.DeepCopy()
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[LabelKey] = LabelValue

	if err := r.Patch(ctx, &pod, client.MergeFrom(original)); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to add label to pod", "label", LabelKey)
		return ctrl.Result{}, err
	}

	// The logger from the context already carries the Pod's name and namespace.
	log.Info("added label to pod", "label", LabelKey+"="+LabelValue)
	return ctrl.Result{}, nil
}

// createOnly passes Create events through and drops Update, Delete and
// Generic events, so the operator reacts only to newly-created Pods.
var createOnly = predicate.Funcs{
	CreateFunc:  func(event.CreateEvent) bool { return true },
	UpdateFunc:  func(event.UpdateEvent) bool { return false },
	DeleteFunc:  func(event.DeleteEvent) bool { return false },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(createOnly)).
		Named("pod").
		Complete(r)
}
