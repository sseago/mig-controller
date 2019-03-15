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

package migrationaio

import (
	"context"
	"reflect"
	"time"

	migrationsv1alpha1 "github.com/fusor/mig-controller/pkg/apis/migrations/v1alpha1"
	velerov1 "github.com/heptio/velero/pkg/apis/velero/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// Add creates a new MigrationAIO Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMigrationAIO{Client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("migrationaio-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MigrationAIO
	err = c.Watch(&source.Kind{Type: &migrationsv1alpha1.MigrationAIO{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create
	// Uncomment watch a Deployment created by MigrationAIO - change this for objects you create
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &migrationsv1alpha1.MigrationAIO{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileMigrationAIO{}

// ReconcileMigrationAIO reconciles a MigrationAIO object
type ReconcileMigrationAIO struct {
	client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a MigrationAIO object and makes changes based on the state read
// and what is in the MigrationAIO.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  The scaffolding writes
// a Deployment as an example
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.openshift.io,resources=migrationaios,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.openshift.io,resources=migrationaios/status,verbs=get;update;patch
func (r *ReconcileMigrationAIO) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the MigrationAIO instance
	instance := &migrationsv1alpha1.MigrationAIO{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// TODO(user): Change this to be the object type created by your controller
	// Define the desired Deployment object
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-deployment",
			Namespace: instance.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"deployment": instance.Name + "-deployment"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"deployment": instance.Name + "-deployment"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
						},
					},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(instance, deploy, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// TODO(user): Change this for the object type created by your controller
	// Check if the Deployment already exists
	found := &appsv1.Deployment{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Deployment", "namespace", deploy.Namespace, "name", deploy.Name)
		err = r.Create(context.TODO(), deploy)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// TODO(user): Change this for the object type created by your controller
	// Update the found object and write the result back if there are any changes
	if !reflect.DeepEqual(deploy.Spec, found.Spec) {
		found.Spec = deploy.Spec
		log.Info("Updating Deployment", "namespace", deploy.Namespace, "name", deploy.Name)
		err = r.Update(context.TODO(), found)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// [DJWHATLE]
	// put ObjectReference to Deployment into all spec fields of watched object
	mySrcCluster := &kapi.ObjectReference{
		APIVersion:      deploy.APIVersion,
		Kind:            deploy.Kind,
		Name:            deploy.Name,
		Namespace:       deploy.Namespace,
		ResourceVersion: deploy.ResourceVersion,
		UID:             deploy.UID,
	}

	currentTime := time.Now()

	instance.Spec.SrcClusterCoordinatesRef = mySrcCluster
	instance.Spec.DestClusterCoordinatesRef = mySrcCluster
	instance.Spec.ScheduledStart = metav1.Time{Time: currentTime}

	// [VELERO BACKUP]

	backup := &velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-backup",
			Namespace: "velero",
		},
		Spec: velerov1.BackupSpec{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
			StorageLocation:    "default",
			TTL:                metav1.Duration{Duration: 720 * time.Hour},
			IncludedNamespaces: []string{"*"},
		},
	}

	// // [CREATE VELERO NAMESPACE IF NEEDED]
	// nsSpec := &kapi.Namespace{ObjectMeta: metav1.ObjectMeta{Name: backup.Namespace}}
	// if err != nil && errors.IsNotFound(err) {
	// 	log.Info("Creating heptio-ark namespace", "namespace", backup.Namespace)
	// 	err = r.Create(context.TODO(), nsSpec)
	// 	if err != nil {
	// 		return reconcile.Result{}, err
	// 	}
	// } else if err != nil {
	// 	return reconcile.Result{}, err
	// }

	// // Create backup if not found
	// if err != nil {
	// 	return reconcile.Result{}, err
	// }

	curBackup := &velerov1.Backup{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}, curBackup)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Velero Backup", "namespace", backup.Namespace, "name", backup.Name)
		err = r.Create(context.TODO(), backup)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	}

	log.Info("Updating MigrationAIO", "namespace", instance.Namespace, "name", instance.Name)
	err = r.Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// [DJWHATLE]

	return reconcile.Result{}, nil
}
