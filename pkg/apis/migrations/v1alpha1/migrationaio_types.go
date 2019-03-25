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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MigrationAIOSpec defines the desired state of MigrationAIO
type MigrationAIOSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	SrcClusterURL   string `json:"srcClusterURL"`
	SrcClusterToken string `json:"srcClusterToken"`

	DestClusterURL   string `json:"destClusterURL"`
	DestClusterToken string `json:"destClusterToken"`

	MigrationNamespaces []string `json:"migrationNamespaces"`
}

// MigrationAIOStatus defines the observed state of MigrationAIO
type MigrationAIOStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Phase      string `json:"phase"`
	Conditions []MigrationCondition
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MigrationAIO is the Schema for the migrationaios API
// +k8s:openapi-gen=true
type MigrationAIO struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigrationAIOSpec   `json:"spec,omitempty"`
	Status MigrationAIOStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MigrationAIOList contains a list of MigrationAIO
type MigrationAIOList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MigrationAIO `json:"items"`
}

// MigrationPhase is a label for the condition of a MigrationAIO at the current time.
type MigrationPhase string

// These are the valid statuses of MigrationAIOs.
const (
	// Migration has not yet started. MigrationPlan has been changed and the controller
	// is validating the new contents of the MigrationPlan.
	MigrationPrecheck MigrationPhase = "MigrationPrecheck"
	// Migration precheck uncovered an issue.
	MigrationPrecheckFailed MigrationPhase = "MigrationPrecheckFailed"
	// Migration has been checked and is scheduled to run at a later time.
	MigrationScheduled MigrationPhase = "ValidMigrationScheduled"
	// Migration is running
	RunningMigration MigrationPhase = "RunningMigration"
	// Migration completed successfully
	MigrationSucceeded MigrationPhase = "MigrationSucceeded"
	// Migration failed while running
	MigrationFailed MigrationPhase = "MigrationFailed"
	// Unexpected state occured, perhaps we can't contact Velero for update
	Unknown MigrationPhase = "Unknown"
)

// MigrationCondition contains details for the current condition of this MigrationAIO resource.
type MigrationCondition struct {
	// Type is the type of the condition.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#pod-conditions
	Type string `json:"type"`

	// Status is the status of the condition.
	// Can be True, False, Unknown.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#pod-conditions
	Status string `json:"status"`

	// Last time we probed the condition.
	// +optional
	LastProbeTime metav1.Time `json:"lastProbeTime,omitempty"`

	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// Unique, one-word, CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

func init() {
	SchemeBuilder.Register(&MigrationAIO{}, &MigrationAIOList{})
}
