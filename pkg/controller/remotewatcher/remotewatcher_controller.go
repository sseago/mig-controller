/*
Copyright 2019 Red Hat Inc.

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

package remotewatcher

import (
	velerov1 "github.com/heptio/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new RemoteWatcher Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
// func Add(mgr manager.Manager) error {
// 	return add(mgr, newReconciler(mgr))
// }
// Add creates a new RemoteWatcher Controller with a source.Channel that will be fed to a reconciler
func Add(mgr manager.Manager, forwardChannel source.Channel) error {
	return add(mgr, newReconciler(mgr, forwardChannel))
}

// newReconciler returns a new reconcile.Reconciler
// func newReconciler(mgr manager.Manager) reconcile.Reconciler {
// 	return &ReconcileRemoteWatcher{Client: mgr.GetClient(), scheme: mgr.GetScheme()}
// }
func newReconciler(mgr manager.Manager, forwardChannel source.Channel) reconcile.Reconciler {
	return &ReconcileRemoteWatcher{Client: mgr.GetClient(), scheme: mgr.GetScheme(), forwardChannel: forwardChannel}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("remotewatcher-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &velerov1.Backup{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &velerov1.Restore{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to RemoteWatcher
	// err = c.Watch(&source.Kind{Type: &migrationsv1alpha1.RemoteWatcher{}}, &handler.EnqueueRequestForObject{})
	// if err != nil {
	// 	return err
	// }

	// TODO(user): Modify this to be the types you create
	// Uncomment watch a Deployment created by RemoteWatcher - change this for objects you create
	// err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
	// 	IsController: true,
	// 	OwnerType:    &migrationsv1alpha1.RemoteWatcher{},
	// })
	// if err != nil {
	// 	return err
	// }

	return nil
}

var _ reconcile.Reconciler = &ReconcileRemoteWatcher{}

// ReconcileRemoteWatcher reconciles a RemoteWatcher object
type ReconcileRemoteWatcher struct {
	client.Client
	scheme         *runtime.Scheme
	forwardChannel source.Channel
}

// Reconcile reads that state of the cluster for a RemoteWatcher object and makes changes based on the state read
// and what is in the RemoteWatcher.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  The scaffolding writes
// a Deployment as an example
// +kubebuilder:rbac:groups=migrations.openshift.io,resources=remotewatchers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.openshift.io,resources=remotewatchers/status,verbs=get;update;patch
func (r *ReconcileRemoteWatcher) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the RemoteWatcher instance
	log.Info("*** REMOTEWATCHER LOOP TRIGGER *** | [namespace]: " + request.Namespace + " | [name]: " + request.Name)

	forwardReconcileRequest(request, r.forwardChannel)
	// instance := &migrationsv1alpha1.RemoteWatcher{}
	// err := r.Get(context.TODO(), request.NamespacedName, instance)
	// if err != nil {
	// 	if errors.IsNotFound(err) {
	// 		// Object not found, return.  Created objects are automatically garbage collected.
	// 		// For additional cleanup logic use finalizers.
	// 		return reconcile.Result{}, nil
	// 	}
	// 	// Error reading the object - requeue the request.
	// 	return reconcile.Result{}, err
	// }

	return reconcile.Result{}, nil
}
