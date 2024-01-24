/*
Copyright 2023 Andrey Inishev.

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

package v1beta1

import (
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/errors"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TankaSpec defines the desired state of Tanka
type TankaSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	SourceRef meta.NamespacedObjectKindReference `json:"sourceRef"`

	// DependsOn may contain a meta.NamespacedObjectReference slice
	// with references to Kustomization resources that must be ready before this
	// Kustomization can be reconciled.
	// +optional
	DependsOn []meta.NamespacedObjectReference `json:"dependsOn,omitempty"`
	// Decrypt Kubernetes secrets before applying them on the cluster.
	// +optional
	Decryption *Decryption       `json:"decryption,omitempty"`
	TLAString  []string          `json:"tlaString"`
	TLACode    map[string]string `json:"tlaCode"`

	Environments []string `json:"environments"`
}

// Decryption defines how decryption is handled for Kubernetes manifests.
type Decryption struct {
	// Provider is the name of the decryption engine.
	// +kubebuilder:validation:Enum=sops
	// +required
	Provider string `json:"provider"`

	// The secret name containing the private OpenPGP keys used for decryption.
	// +optional
	SecretRef *meta.LocalObjectReference `json:"secretRef,omitempty"`
}

func (s *TankaSpec) UnwrapSource() (sourcev1.Source, error) {
	switch s.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		return &sourcev1.GitRepository{}, nil
	case sourcev1beta2.OCIRepositoryKind:
		return &sourcev1beta2.OCIRepository{}, nil
	case sourcev1beta2.BucketKind:
		return &sourcev1beta2.Bucket{}, nil
	}

	return nil, &errors.UnsupportedResourceKindError{
		Kind: s.SourceRef.Kind, NamespacedName: types.NamespacedName{
			Name: s.SourceRef.Name, Namespace: s.SourceRef.Namespace,
		},
	}
}

// TankaStatus defines the observed state of Tanka
type TankaStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	meta.ReconcileRequestStatus `json:",inline"`

	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The last successfully applied revision.
	// Equals the Revision of the applied Artifact from the referenced Source.
	// +optional
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`

	// LastAttemptedRevision is the revision of the last reconciliation attempt.
	// +optional
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`

	// Inventory contains the list of Kubernetes resource object references that
	// have been successfully applied.
	// +optional
	Inventory *ResourceInventory `json:"inventory,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Tanka is the Schema for the tankas API
type Tanka struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TankaSpec   `json:"spec,omitempty"`
	Status TankaStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TankaList contains a list of Tanka
type TankaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tanka `json:"items"`
}

// GetDependsOn returns the list of dependencies across-namespaces.
func (in Tanka) GetDependsOn() []meta.NamespacedObjectReference {
	return in.Spec.DependsOn
}

func init() {
	SchemeBuilder.Register(&Tanka{}, &TankaList{})
}
