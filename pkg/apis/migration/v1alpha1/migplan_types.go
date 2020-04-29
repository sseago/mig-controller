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

package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	appsv1 "github.com/openshift/api/apps/v1"
	imagev1 "github.com/openshift/api/image/v1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	kapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sLabels "k8s.io/apimachinery/pkg/labels"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	pvdr "github.com/konveyor/mig-controller/pkg/cloudprovider"
	migref "github.com/konveyor/mig-controller/pkg/reference"
	"github.com/konveyor/mig-controller/pkg/settings"
)

var Settings = &settings.Settings

// Cache Indexes.
const (
	ClosedIndexField = "closed"
)

// MigPlanHook hold a referene to a MigHook along with the desired phase to run it in
type MigPlanHook struct {
	Reference          *kapi.ObjectReference `json:"reference"`
	Phase              string                `json:"phase"`
	ExecutionNamespace string                `json:"executionNamespace"`
	ServiceAccount     string                `json:"serviceAccount"`
}

// MigPlanSpec defines the desired state of MigPlan
type MigPlanSpec struct {
	UnhealthyResources
	PersistentVolumes `json:",inline"`
	Namespaces        []string              `json:"namespaces,omitempty"`
	SrcMigClusterRef  *kapi.ObjectReference `json:"srcMigClusterRef,omitempty"`
	DestMigClusterRef *kapi.ObjectReference `json:"destMigClusterRef,omitempty"`
	MigStorageRef     *kapi.ObjectReference `json:"migStorageRef,omitempty"`
	Closed            bool                  `json:"closed,omitempty"`
	Hooks             []MigPlanHook         `json:"hooks,omitempty"`
}

// MigPlanStatus defines the observed state of MigPlan
type MigPlanStatus struct {
	Conditions
	Incompatible   `json:",inline"`
	ObservedDigest string `json:"observedDigest,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MigPlan is the Schema for the migplans API
// +k8s:openapi-gen=true
type MigPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigPlanSpec   `json:"spec,omitempty"`
	Status MigPlanStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MigPlanList contains a list of MigPlan
type MigPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MigPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MigPlan{}, &MigPlanList{})
}

// GetSourceCluster - Get the referenced source cluster.
// Returns `nil` when the reference cannot be resolved.
func (r *MigPlan) GetSourceCluster(client k8sclient.Client) (*MigCluster, error) {
	return GetCluster(client, r.Spec.SrcMigClusterRef)
}

// GetDestinationCluster - Get the referenced destination cluster.
// Returns `nil` when the reference cannot be resolved.
func (r *MigPlan) GetDestinationCluster(client k8sclient.Client) (*MigCluster, error) {
	return GetCluster(client, r.Spec.DestMigClusterRef)
}

// GetStorage - Get the referenced storage.
// Returns `nil` when the reference cannot be resolved.
func (r *MigPlan) GetStorage(client k8sclient.Client) (*MigStorage, error) {
	return GetStorage(client, r.Spec.MigStorageRef)
}

// Resources referenced by the plan.
// Contains all of the fetched referenced resources.
type PlanResources struct {
	MigPlan        *MigPlan
	MigStorage     *MigStorage
	SrcMigCluster  *MigCluster
	DestMigCluster *MigCluster
}

// GetRefResources gets referenced resources from a MigPlan.
func (r *MigPlan) GetRefResources(client k8sclient.Client) (*PlanResources, error) {
	// MigStorage
	storage, err := r.GetStorage(client)
	if err != nil {
		return nil, err
	}
	if storage == nil {
		return nil, errors.New("storage not found")
	}

	// SrcMigCluster
	srcMigCluster, err := r.GetSourceCluster(client)
	if err != nil {
		return nil, err
	}
	if srcMigCluster == nil {
		return nil, errors.New("source cluster not found")
	}

	// DestMigCluster
	destMigCluster, err := r.GetDestinationCluster(client)
	if err != nil {
		return nil, err
	}
	if destMigCluster == nil {
		return nil, errors.New("destination cluster not found")
	}

	resources := &PlanResources{
		MigPlan:        r,
		MigStorage:     storage,
		SrcMigCluster:  srcMigCluster,
		DestMigCluster: destMigCluster,
	}

	return resources, nil
}

// Set the MigPlan Status to closed
func (r *MigPlan) SetClosed() {
	r.Spec.Closed = true
}

// Get list of migrations associated with the plan.
// Sorted by created timestamp with final migrations grouped last.
func (r *MigPlan) ListMigrations(client k8sclient.Client) ([]*MigMigration, error) {
	stage := []*MigMigration{}
	final := []*MigMigration{}
	qlist := MigMigrationList{}

	// Search
	err := client.List(
		context.TODO(),
		k8sclient.MatchingField(
			PlanIndexField,
			fmt.Sprintf("%s/%s", r.Namespace, r.Name)),
		&qlist)
	if err != nil {
		return nil, err
	}
	for i := range qlist.Items {
		migration := qlist.Items[i]
		if migration.Spec.Stage {
			stage = append(stage, &migration)
		} else {
			final = append(final, &migration)
		}
	}

	// Sort by creation timestamp.
	sort.Slice(
		stage,
		func(i, j int) bool {
			a := stage[i].ObjectMeta.CreationTimestamp
			b := stage[j].ObjectMeta.CreationTimestamp
			return a.Before(&b)
		})
	sort.Slice(
		final,
		func(i, j int) bool {
			a := final[i].ObjectMeta.CreationTimestamp
			b := final[j].ObjectMeta.CreationTimestamp
			return a.Before(&b)
		})

	list := append(stage, final...)
	return list, nil
}

// ListTemplatePods - get list of pod templates, associated with a plan resource
func (r *MigPlan) ListTemplatePods(client k8sclient.Client) ([]corev1.Pod, error) {
	pods := []corev1.Pod{}
	for _, template := range DefaultTemplates {
		// Get resources
		list := unstructured.UnstructuredList{}
		templateGVK := template.GroupKind().WithVersion("")
		list.SetGroupVersionKind(templateGVK)
		err := client.List(context.TODO(), &k8sclient.ListOptions{}, &list)
		if err != nil {
			return nil, err
		}
		resources := []unstructured.Unstructured{}
		for _, resource := range list.Items {
			for _, ns := range r.GetSourceNamespaces() {
				if resource.GetNamespace() == ns {
					resources = append(resources, resource)
					break
				}
			}
		}

		for _, resource := range resources {
			podTemplate := corev1.PodTemplateSpec{}
			unstructuredTemplate, found, err := unstructured.NestedMap(resource.Object, template.Path()...)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("Template path %s was not found on resource: %s", template.TemplatePath, template.Resource)
			}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredTemplate, &podTemplate)
			if err != nil {
				return nil, fmt.Errorf("Unable to convert resource filed '%s' to 'PodTemplateSpec': %v", template.TemplatePath, err)
			}
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resource.GetName(),
					Namespace: resource.GetNamespace(),
				},
				Spec: podTemplate.Spec,
			}
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

//
// Registry
//

// Registry label for controller-created migration registry resources
const (
	MigrationRegistryLabel        = "migration-registry"
	MigrationRegistryDefaultImage = "registry:2"
	MigrationRegistryImageEnvVar  = "MIGRATION_REGISTRY_IMAGE"
)

// Return the image reference for the migration registry (defaults to "registry:2")
func migRegistryImageRef() string {
	envImage := os.Getenv(MigrationRegistryImageEnvVar)
	if envImage == "" {
		return MigrationRegistryDefaultImage
	}
	return envImage
}

// Build a credentials Secret as desired for the source cluster.
func (r *MigPlan) BuildRegistrySecret(client k8sclient.Client, storage *MigStorage) (*kapi.Secret, error) {
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	secret := &kapi.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Labels:       labels,
			GenerateName: "registry-" + string(r.UID) + "-",
			Namespace:    VeleroNamespace,
		},
	}
	return secret, r.UpdateRegistrySecret(client, storage, secret)
}

// Update a Registry credentials secret as desired for the specified cluster.
func (r *MigPlan) UpdateRegistrySecret(client k8sclient.Client, storage *MigStorage, registrySecret *kapi.Secret) error {
	credSecret, err := storage.GetBackupStorageCredSecret(client)
	if err != nil {
		return err
	}
	if credSecret == nil {
		return errors.New("Credentials secret not found.")
	}
	provider := storage.GetBackupStorageProvider()
	provider.UpdateRegistrySecret(credSecret, registrySecret)
	return nil
}

// Get an existing credentials Secret on the source cluster.
func (r *MigPlan) GetRegistrySecret(client k8sclient.Client) (*kapi.Secret, error) {
	list := kapi.SecretList{}
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Determine if two registry credentials secrets are equal.
// Returns `true` when equal.
func (r *MigPlan) EqualsRegistrySecret(a, b *kapi.Secret) bool {
	return reflect.DeepEqual(a.Data, b.Data)
}

// Build a Registry ImageStream as desired for the source cluster.
func (r *MigPlan) BuildRegistryImageStream(name string) (*imagev1.ImageStream, error) {
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	labels["app"] = name
	imagestream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    labels,
			Name:      name,
			Namespace: VeleroNamespace,
		},
	}
	err := r.UpdateRegistryImageStream(imagestream)
	return imagestream, err
}

// Update a Registry ImageStream as desired for the specified cluster.
func (r *MigPlan) UpdateRegistryImageStream(imagestream *imagev1.ImageStream) error {
	imagestream.Spec = imagev1.ImageStreamSpec{
		LookupPolicy: imagev1.ImageLookupPolicy{Local: false},
		Tags: []imagev1.TagReference{
			imagev1.TagReference{
				Name: "2",
				Annotations: map[string]string{
					"openshift.io/imported-from": migRegistryImageRef(),
				},
				From: &kapi.ObjectReference{
					Kind: "DockerImage",
					Name: migRegistryImageRef(),
				},
				Generation:      nil,
				ImportPolicy:    imagev1.TagImportPolicy{},
				ReferencePolicy: imagev1.TagReferencePolicy{Type: ""},
			},
		},
	}
	return nil
}

// Get an existing registry ImageStream on the specifiedcluster.
func (r *MigPlan) GetRegistryImageStream(client k8sclient.Client) (*imagev1.ImageStream, error) {
	list := imagev1.ImageStreamList{}
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Determine if two imagestreams are equal.
// Returns `true` when equal.
func (r *MigPlan) EqualsRegistryImageStream(a, b *imagev1.ImageStream) bool {
	if len(a.Spec.Tags) != len(b.Spec.Tags) {
		return false
	}
	for i, tag := range a.Spec.Tags {
		if !(reflect.DeepEqual(tag.Name, b.Spec.Tags[i].Name) &&
			reflect.DeepEqual(tag.From, b.Spec.Tags[i].From)) {
			return false
		}
	}
	return true
}

// Build a Registry DeploymentConfig as desired for the source cluster.
func (r *MigPlan) BuildRegistryDC(storage *MigStorage, proxySecret *kapi.Secret, name, dirName string) (*appsv1.DeploymentConfig, error) {
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	labels["app"] = name
	deploymentconfig := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    labels,
			Name:      name,
			Namespace: VeleroNamespace,
		},
	}
	err := r.UpdateRegistryDC(storage, deploymentconfig, proxySecret, name, dirName)
	return deploymentconfig, err
}

// Get registry proxy secret for registry DC
// Returns nil if secret isn't found or no configuration exists
func (r *MigPlan) GetRegistryProxySecret(client k8sclient.Client) (*kapi.Secret, error) {
	list := kapi.SecretList{}
	selector := k8sLabels.SelectorFromSet(map[string]string{
		"migration-proxy-config": "true",
	})
	err := client.List(
		context.TODO(),
		&k8sclient.ListOptions{
			Namespace:     VeleroNamespace,
			LabelSelector: selector,
		},
		&list,
	)
	// If error listing secrets return no env vars
	if err != nil {
		return nil, err
	}
	// If no secrets found, return nil and no error
	// This means no proxy is configured
	if len(list.Items) == 0 {
		return nil, nil
	}
	if len(list.Items) > 1 {
		return nil, errors.New("found multiple proxy config secrets")
	}

	return &list.Items[0], nil
}

// Update a Registry DeploymentConfig as desired for the specified cluster.
func (r *MigPlan) UpdateRegistryDC(storage *MigStorage, deploymentconfig *appsv1.DeploymentConfig, proxySecret *kapi.Secret, name, dirName string) error {
	envFrom := []kapi.EnvFromSource{}
	// If Proxy secret exists, set env from it
	if proxySecret != nil {
		source := kapi.EnvFromSource{
			SecretRef: &kapi.SecretEnvSource{
				LocalObjectReference: kapi.LocalObjectReference{
					Name: proxySecret.Name,
				},
			},
		}
		envFrom = append(envFrom, source)
	}
	deploymentconfig.Spec = appsv1.DeploymentConfigSpec{
		Replicas: 1,
		Selector: map[string]string{
			"app":              name,
			"deploymentconfig": name,
		},
		Strategy: appsv1.DeploymentStrategy{Resources: kapi.ResourceRequirements{}},
		Template: &kapi.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Time{},
				Labels: map[string]string{
					"app":              name,
					"deploymentconfig": name,
				},
			},
			Spec: kapi.PodSpec{
				Containers: []kapi.Container{
					kapi.Container{
						EnvFrom: envFrom,
						Image:   migRegistryImageRef(),
						Name:    "registry",
						Ports: []kapi.ContainerPort{
							kapi.ContainerPort{
								ContainerPort: 5000,
								Protocol:      kapi.ProtocolTCP,
							},
						},
						Resources: kapi.ResourceRequirements{},
						VolumeMounts: []kapi.VolumeMount{
							kapi.VolumeMount{
								MountPath: "/var/lib/registry",
								Name:      "volume-1",
							},
						},
					},
				},
				Volumes: []kapi.Volume{
					kapi.Volume{
						Name:         "volume-1",
						VolumeSource: kapi.VolumeSource{EmptyDir: &kapi.EmptyDirVolumeSource{}},
					},
				},
			},
		},
		Test: false,
		Triggers: appsv1.DeploymentTriggerPolicies{
			appsv1.DeploymentTriggerPolicy{
				Type: appsv1.DeploymentTriggerOnConfigChange,
			},
		},
	}
	provider := storage.GetBackupStorageProvider()
	provider.UpdateRegistryDC(deploymentconfig, name, dirName)

	return nil
}

// Get an existing registry DeploymentConfig on the specifiedcluster.
func (r *MigPlan) GetRegistryDC(client k8sclient.Client) (*appsv1.DeploymentConfig, error) {
	list := appsv1.DeploymentConfigList{}
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Determine if two deploymentconfigs are equal.
// Returns `true` when equal.
func (r *MigPlan) EqualsRegistryDC(a, b *appsv1.DeploymentConfig) bool {
	if !(reflect.DeepEqual(a.Spec.Replicas, b.Spec.Replicas) &&
		reflect.DeepEqual(a.Spec.Selector, b.Spec.Selector) &&
		reflect.DeepEqual(a.Spec.Template.ObjectMeta, b.Spec.Template.ObjectMeta) &&
		reflect.DeepEqual(a.Spec.Template.Spec.Volumes, b.Spec.Template.Spec.Volumes) &&
		len(a.Spec.Template.Spec.Containers) == len(b.Spec.Template.Spec.Containers) &&
		len(a.Spec.Triggers) == len(b.Spec.Triggers)) {
		return false
	}
	for i, container := range a.Spec.Template.Spec.Containers {
		if !(reflect.DeepEqual(container.Env, b.Spec.Template.Spec.Containers[i].Env) &&
			reflect.DeepEqual(container.Name, b.Spec.Template.Spec.Containers[i].Name) &&
			reflect.DeepEqual(container.Ports, b.Spec.Template.Spec.Containers[i].Ports) &&
			reflect.DeepEqual(container.VolumeMounts, b.Spec.Template.Spec.Containers[i].VolumeMounts)) {
			return false
		}
	}
	for i, trigger := range a.Spec.Triggers {
		if !reflect.DeepEqual(trigger, b.Spec.Triggers[i]) {
			return false
		}
	}
	return true
}

// Build a Registry Service as desired for the specified cluster.
func (r *MigPlan) BuildRegistryService(name string) (*kapi.Service, error) {
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	labels["app"] = name
	service := &kapi.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    labels,
			Name:      name,
			Namespace: VeleroNamespace,
		},
	}
	err := r.UpdateRegistryService(service, name)
	return service, err
}

// Update a Registry Service as desired for the specified cluster.
func (r *MigPlan) UpdateRegistryService(service *kapi.Service, name string) error {
	service.Spec = kapi.ServiceSpec{
		Ports: []kapi.ServicePort{
			kapi.ServicePort{
				Name:       "5000-tcp",
				Port:       5000,
				Protocol:   kapi.ProtocolTCP,
				TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 5000},
			},
		},
		Selector: map[string]string{
			"app":              name,
			"deploymentconfig": name,
		},
	}
	return nil
}

// Get an existing registry Service on the specifiedcluster.
func (r *MigPlan) GetRegistryService(client k8sclient.Client) (*kapi.Service, error) {
	list := kapi.ServiceList{}
	labels := r.GetCorrelationLabels()
	labels[MigrationRegistryLabel] = string(r.UID)
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Determine if two services are equal.
// Returns `true` when equal.
func (r *MigPlan) EqualsRegistryService(a, b *kapi.Service) bool {
	return reflect.DeepEqual(a.Spec.Ports, b.Spec.Ports) &&
		reflect.DeepEqual(a.Spec.Selector, b.Spec.Selector)
}

//
// Storage
//

// Get an associated BSL by Label search.
// Returns `nil` when not found.
func (r *MigPlan) GetBSL(client k8sclient.Client) (*velero.BackupStorageLocation, error) {
	list := velero.BackupStorageLocationList{}
	labels := r.GetCorrelationLabels()
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Get an associated VSL by Label search.
// Returns `nil` when not found.
func (r *MigPlan) GetVSL(client k8sclient.Client) (*velero.VolumeSnapshotLocation, error) {
	list := velero.VolumeSnapshotLocationList{}
	labels := r.GetCorrelationLabels()
	err := client.List(
		context.TODO(),
		k8sclient.MatchingLabels(labels),
		&list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) > 0 {
		return &list.Items[0], nil
	}

	return nil, nil
}

// Get the cloud credentials secret by labels for the provider.
func (r *MigPlan) GetCloudSecret(client k8sclient.Client, provider pvdr.Provider) (*kapi.Secret, error) {
	return GetSecret(
		client,
		&kapi.ObjectReference{
			Namespace: VeleroNamespace,
			Name:      provider.GetCloudSecretName(),
		})
}

// GetSourceNamespaces get source namespaces without mapping
func (r *MigPlan) GetSourceNamespaces() []string {
	includedNamespaces := []string{}
	for _, namespace := range r.Spec.Namespaces {
		namespace = strings.Split(namespace, ":")[0]
		includedNamespaces = append(includedNamespaces, namespace)
	}

	return includedNamespaces
}

// GetDestinationNamespaces get destination namespaces without mapping
func (r *MigPlan) GetDestinationNamespaces() []string {
	includedNamespaces := []string{}
	for _, namespace := range r.Spec.Namespaces {
		namespaces := strings.Split(namespace, ":")
		if len(namespaces) > 1 {
			includedNamespaces = append(includedNamespaces, namespaces[1])
		} else {
			includedNamespaces = append(includedNamespaces, namespaces[0])
		}
	}

	return includedNamespaces
}

// Get whether the plan conflicts with another.
// Plans conflict when:
//   - Have any of the clusters in common.
//   - Hand any of the namespaces in common.
func (r *MigPlan) HasConflict(plan *MigPlan) bool {
	if !migref.RefEquals(r.Spec.SrcMigClusterRef, plan.Spec.SrcMigClusterRef) &&
		!migref.RefEquals(r.Spec.DestMigClusterRef, plan.Spec.DestMigClusterRef) &&
		!migref.RefEquals(r.Spec.SrcMigClusterRef, plan.Spec.DestMigClusterRef) &&
		!migref.RefEquals(r.Spec.DestMigClusterRef, plan.Spec.SrcMigClusterRef) {
		return false
	}
	nsMap := map[string]bool{}
	for _, name := range plan.Spec.Namespaces {
		nsMap[name] = true
	}
	for _, name := range r.Spec.Namespaces {
		if _, found := nsMap[name]; found {
			return true
		}
	}

	return false
}

//
//
// PV list
//
//

// PV Actions.
const (
	PvMoveAction = "move"
	PvCopyAction = "copy"
	PvSkipAction = "skip"
)

// PV Copy Methods.
const (
	PvFilesystemCopyMethod = "filesystem"
	PvSnapshotCopyMethod   = "snapshot"
)

// Name - The PV name.
// Capacity - The PV storage capacity.
// StorageClass - The PV storage class name.
// Supported - Lists of what is supported.
// Selection - Choices made from supported.
// PVC - Associated PVC.
// NFS - NFS properties.
// staged - A PV has been explicitly added/updated.
type PV struct {
	Name         string                `json:"name,omitempty"`
	Capacity     resource.Quantity     `json:"capacity,omitempty"`
	StorageClass string                `json:"storageClass,omitempty"`
	Supported    Supported             `json:"supported"`
	Selection    Selection             `json:"selection"`
	PVC          PVC                   `json:"pvc,omitempty"`
	NFS          *kapi.NFSVolumeSource `json:"-"`
	staged       bool
}

// PVC
type PVC struct {
	Namespace    string                            `json:"namespace,omitempty" protobuf:"bytes,3,opt,name=namespace"`
	Name         string                            `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	AccessModes  []kapi.PersistentVolumeAccessMode `json:"accessModes,omitempty" protobuf:"bytes,1,rep,name=accessModes,casttype=PersistentVolumeAccessMode"`
	HasReference bool                              `json:"hasReference,omitempty"`
}

// Supported
// Actions     - The list of supported actions
// CopyMethods - The list of supported copy methods
type Supported struct {
	Actions     []string `json:"actions"`
	CopyMethods []string `json:"copyMethods"`
}

// Selection
// Action - The PV migration action (move|copy|skip)
// StorageClass - The PV storage class name to use in the destination cluster.
// AccessMode   - The PV access mode to use in the destination cluster, if different from src PVC AccessMode
// CopyMethod   - The PV copy method to use ('filesystem' for restic copy, or 'snapshot' for velero snapshot plugin)
// Verify       - Whether or not to verify copied volume data if CopyMethod is 'filesystem'
type Selection struct {
	Action       string                          `json:"action,omitempty"`
	StorageClass string                          `json:"storageClass,omitempty"`
	AccessMode   kapi.PersistentVolumeAccessMode `json:"accessMode,omitempty" protobuf:"bytes,1,rep,name=accessMode,casttype=PersistentVolumeAccessMode"`
	CopyMethod   string                          `json:"copyMethod,omitempty"`
	Verify       bool                            `json:"verify,omitempty"`
}

// Update the PV with another.
func (r *PV) Update(pv PV) {
	r.StorageClass = pv.StorageClass
	r.Supported.Actions = pv.Supported.Actions
	r.Supported.CopyMethods = pv.Supported.CopyMethods
	r.Capacity = pv.Capacity
	r.PVC = pv.PVC
	r.NFS = pv.NFS
	if len(r.Supported.Actions) == 1 {
		r.Selection.Action = r.Supported.Actions[0]
	}
	r.staged = true
}

// Collection of PVs
// List - The collection of PVs.
// index - List index.
// --------
// Example:
// plan.Spec.BeginPvStaging()
// plan.Spec.AddPv(pvA)
// plan.Spec.AddPv(pvB)
// plan.Spec.AddPv(pvC)
// plan.Spec.EndPvStaging()
//
type PersistentVolumes struct {
	List    []PV `json:"persistentVolumes,omitempty"`
	index   map[string]int
	staging bool
}

// Allocate collections.
func (r *PersistentVolumes) init() {
	if r.List == nil {
		r.List = []PV{}
	}
	if r.index == nil {
		r.buildIndex()
	}
}

// Build the index.
func (r *PersistentVolumes) buildIndex() {
	r.index = map[string]int{}
	for i, pv := range r.List {
		r.index[pv.Name] = i
	}
}

// Begin staging PVs.
func (r *PersistentVolumes) BeginPvStaging() {
	r.init()
	r.staging = true
	for i := range r.List {
		pv := &r.List[i]
		pv.staged = false
	}
}

// End staging PVs and delete un-staged PVs from the list.
// The PVs are sorted by Name.
func (r *PersistentVolumes) EndPvStaging() {
	r.init()
	r.staging = false
	kept := []PV{}
	for _, pv := range r.List {
		if pv.staged {
			kept = append(kept, pv)
		}
	}
	sort.Slice(
		kept,
		func(i, j int) bool {
			return kept[i].Name < kept[j].Name
		})
	r.List = kept
	r.buildIndex()
}

// Find a PV
func (r *PersistentVolumes) FindPv(pv PV) *PV {
	r.init()
	i, found := r.index[pv.Name]
	if found {
		return &r.List[i]
	}

	return nil
}

// Add (or update) Pv to the collection.
func (r *PersistentVolumes) AddPv(pv PV) {
	r.init()
	pv.staged = true
	foundPv := r.FindPv(pv)
	if foundPv == nil {
		r.List = append(r.List, pv)
		r.index[pv.Name] = len(r.List) - 1
	} else {
		foundPv.Update(pv)
	}
}

// Delete a PV from the collection.
func (r *PersistentVolumes) DeletePv(names ...string) {
	r.init()
	for _, name := range names {
		i, found := r.index[name]
		if found {
			r.List = append(r.List[:i], r.List[i+1:]...)
			r.buildIndex()
		}
	}
}

// Convert name to a DNS_LABEL-compliant string
// DNS_LABEL:  This is a string, no more than 63 characters long, that conforms
//     to the definition of a "label" in RFCs 1035 and 1123. This is captured
//     by the following regex:
//         [a-z0-9]([-a-z0-9]*[a-z0-9])?
func toDnsLabel(name string) (string, error) {
	// keep lowercase alphanumeric and hyphen
	reg, err := regexp.Compile("[^-a-z0-9]+")
	if err != nil {
		return "", err
	}
	processedName := reg.ReplaceAllString(strings.ToLower(name), "")
	// strip initial and trailing hyphens
	stripEndsReg, err := regexp.Compile("(^-+|-+$)")
	if err != nil {
		return "", err
	}
	endsStripped := stripEndsReg.ReplaceAllString(processedName, "")
	return endsStripped, nil
}
