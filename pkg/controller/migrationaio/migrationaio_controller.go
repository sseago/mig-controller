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

	migrationsv1alpha1 "github.com/fusor/mig-controller/pkg/apis/migrations/v1alpha1"
	velerov1 "github.com/heptio/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	// // Uncomment watch a Velero Backup created by MigrationAIO - change this for objects you create
	// err = c.Watch(&source.Kind{Type: &velerov1.Backup{}}, &handler.EnqueueRequestForOwner{
	// 	IsController: true,
	// 	OwnerType:    &migrationsv1alpha1.MigrationAIO{},
	// })
	// if err != nil {
	// 	return err
	// }

	// Watch for changes to Velero Backup, enqueue request for MigrationAIO that owns
	// err = c.Watch(&source.Kind{Type: &velerov1.Backup{}}, &handler.EnqueueRequestForObject{})
	err = c.Watch(&source.Kind{Type: &velerov1.Backup{}}, &EnqueueRequestForAnnotation{Type: "MigrationAIO"})
	if err != nil {
		return err
	}

	// Watch for changes to Velero Restore
	// err = c.Watch(&source.Kind{Type: &velerov1.Restore{}}, &handler.EnqueueRequestForObject{})
	err = c.Watch(&source.Kind{Type: &velerov1.Restore{}}, &EnqueueRequestForAnnotation{Type: "MigrationAIO"})
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
	log.Info("*** RECONCILE LOOP TRIGGER *** | [namespace]: " + request.Namespace + " | [name]: " + request.Name)
	// Fetch the MigrationAIO instance
	instance := &migrationsv1alpha1.MigrationAIO{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			log.Info("Exit 1 - Got reconcile event for MigrationAIO that no longer exists...")
			return reconcile.Result{Requeue: false}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Exit 2")
		return reconcile.Result{}, err
	}

	veleroNs := "velero"
	// ################################################
	// # Create a Velero 'Backup' on the source cluster
	// ################################################
	nsToBackup := instance.Spec.MigrationNamespaces
	newBackup := getVeleroBackup(veleroNs, instance.Name+"-backup", nsToBackup)

	// Set controller reference on 'Backup' object so that we can watch it
	err = controllerutil.SetControllerReference(instance, newBackup, r.scheme)
	if err != nil {
		log.Error(err, "SetControllerReference fail on newBackup")
		return reconcile.Result{}, err
	}

	// Check if a 'Backup' resource already exists
	existingBackup := &velerov1.Backup{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: newBackup.Name, Namespace: newBackup.Namespace}, existingBackup)
	if err != nil {
		if errors.IsNotFound(err) {
			// Backup not found
			newBackup = annotateBackupWithMigrationRef(newBackup, instance)
			err = r.Create(context.TODO(), newBackup)
			if err != nil {
				log.Error(err, "Exit 3: Failed to CREATE Velero Backup")
				return reconcile.Result{}, nil
			}
			log.Info("Velero Backup CREATED successfully")
		}
		// Error reading the 'Backup' object - requeue the request.
		log.Error(err, "Exit 4: Requeueing")
		return reconcile.Result{}, err
	}

	if !reflect.DeepEqual(existingBackup.Spec, newBackup.Spec) {
		// Send "Create" action for Velero Backup to K8s API
		existingBackup.Spec = newBackup.Spec
		existingBackup = annotateBackupWithMigrationRef(existingBackup, instance)
		err = r.Update(context.TODO(), existingBackup)
		if err != nil {
			log.Error(err, "Failed to UPDATE Velero Backup")
			return reconcile.Result{}, nil
		}
		log.Info("Velero Backup UPDATED successfully")
	} else {
		log.Info("Velero Backup EXISTS already")
	}

	// ######################################################
	// # Create a Velero 'Restore' on the destination cluster
	// ######################################################
	destClusterURL := instance.Spec.DestClusterURL
	destClusterToken := instance.Spec.DestClusterToken

	remoteK8sClient, err := getControllerRuntimeClient(destClusterURL, destClusterToken)
	if err != nil {
		log.Error(err, "Failed to GET remoteK8sClient")
		return reconcile.Result{}, nil
	}

	newRestore := getVeleroRestore(veleroNs, instance.Name+"-restore", "nginx-backup")

	// *** TODO - check if restore already exists before attempting to create
	existingRestore := &velerov1.Restore{}
	err = remoteK8sClient.Get(context.TODO(), types.NamespacedName{Name: newRestore.Name, Namespace: newRestore.Namespace}, existingRestore)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Velero Restore NOT FOUND, creating...")
			// Send "Create" action for Velero Backup to K8s API
			newRestore = annotateRestoreWithMigrationRef(newRestore, instance)
			err = remoteK8sClient.Create(context.TODO(), newRestore)
			if err != nil {
				log.Error(err, "Failed to CREATE Velero Restore on remote cluster")
				return reconcile.Result{}, nil
			}
			log.Info("Velero Restore CREATED successfully on remote cluster")
			return reconcile.Result{}, nil
		}
	}
	if !reflect.DeepEqual(existingRestore.Spec, newRestore.Spec) {
		existingRestore.Spec = newRestore.Spec
		existingRestore = annotateRestoreWithMigrationRef(existingRestore, instance)
		err = remoteK8sClient.Update(context.TODO(), existingRestore)
		if err != nil {
			log.Error(err, "Failed to UPDATE Velero Restore")
			return reconcile.Result{}, nil
		}
		log.Info("Velero Restore UPDATED successfully")
	} else {
		log.Info("Velero Restore EXISTS already")
	}

	// TODO
	//  - subscribe to watch events on Velero restores that we create
	//  - subscribe to watch events on Velero backups that we create

	// Mark BackupPhase from Velero Backup on MigrationAIO object
	// Mark RestorePhase from Velero Restore on MigrationAIO object

	return reconcile.Result{}, nil
}
