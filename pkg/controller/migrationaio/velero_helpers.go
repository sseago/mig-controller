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
	"fmt"
	"time"

	migrationsv1alpha1 "github.com/fusor/mig-controller/pkg/apis/migrations/v1alpha1"
	velerov1 "github.com/heptio/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getControllerRuntimeClient(clusterURL string, bearerToken string) (c client.Client, err error) {
	clusterConfig := &rest.Config{
		Host:        clusterURL,
		BearerToken: bearerToken,
	}
	clusterConfig.Insecure = true

	c, err = client.New(clusterConfig, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, err
	}

	return c, nil
}

func getVeleroBackup(ns string, name string, backupNamespaces []string) *velerov1.Backup {
	backup := &velerov1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: velerov1.BackupSpec{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
			StorageLocation:    "default",
			TTL:                metav1.Duration{Duration: 720 * time.Hour},
			IncludedNamespaces: backupNamespaces,
		},
	}
	return backup
}

func getVeleroRestore(ns string, name string, backupUniqueName string) *velerov1.Restore {
	restorePVs := true
	restore := &velerov1.Restore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: velerov1.RestoreSpec{
			BackupName: backupUniqueName,
			RestorePVs: &restorePVs,
		},
	}

	return restore
}

// func annotateChildWithParentRef(childObject metav1.Object, parentObject metav1.Object, parentMeta metav1.TypeMeta) metav1.Object {
// 	// from enqueue_annotation.go
// 	// NamespacedNameAnnotation
// 	// TypeAnnotation
// 	// annotate with format namespace/name

// 	// Get pieces needed to build annotation
// 	parentNamespace := parentObject.GetNamespace()
// 	parentName := parentObject.GetName()
// 	parentKind := parentMeta.Kind // use GVK instead?

// 	// Build namespaced name annotation string
// 	nsName := fmt.Sprintf("%v/%v", parentNamespace, parentName)

// 	// Get annotations from child object
// 	childAnno := childObject.GetAnnotations()

// 	// Modify child annotations to reference parent object
// 	childAnno[NamespacedNameAnnotation] = nsName
// 	childAnno[TypeAnnotation] = parentKind

// 	// Set new parent reference back on child object
// 	childObject.SetAnnotations(childAnno)

// 	return childObject
// }

func annotateBackupWithMigrationRef(child *velerov1.Backup, parent *migrationsv1alpha1.MigrationAIO) *velerov1.Backup {
	// Build namespaced name annotation string
	parentNamespacedName := fmt.Sprintf("%v/%v", parent.Namespace, parent.Name)
	parentKind := parent.TypeMeta.Kind

	// Get and modify child annotations
	annotations := child.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[NamespacedNameAnnotation] = parentNamespacedName
	annotations[TypeAnnotation] = parentKind
	child.SetAnnotations(annotations)

	return child
}

func annotateRestoreWithMigrationRef(child *velerov1.Restore, parent *migrationsv1alpha1.MigrationAIO) *velerov1.Restore {
	// Build namespaced name annotation string
	parentNamespacedName := fmt.Sprintf("%v/%v", parent.Namespace, parent.Name)
	parentKind := parent.TypeMeta.Kind

	// Get and modify child annotations
	annotations := child.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[NamespacedNameAnnotation] = parentNamespacedName
	annotations[TypeAnnotation] = parentKind
	child.SetAnnotations(annotations)

	return child
}
