// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package configpublisher

import (
	"context"
	"errors"
	"fmt"
	"slices"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// createOrUpdateRuntimeConfig creates or updates the HAProxyCfg resource.
func (p *Publisher) createOrUpdateRuntimeConfig(ctx context.Context, req *PublishRequest) (*haproxyv1alpha1.HAProxyCfg, error) {
	name := runtimeConfigResourceName(req.TemplateConfigName, req.NameSuffix)
	runtimeConfig := p.buildRuntimeConfig(name, req)

	var result *haproxyv1alpha1.HAProxyCfg
	firstAttempt := true
	err := retry.OnError(retry.DefaultRetry, retriableWrite, func() error {
		existing, err := p.runtimeConfigToWrite(ctx, req.TemplateConfigNamespace, name, runtimeConfig, firstAttempt)
		firstAttempt = false
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing runtime config: %w", err)
			}
			// Create new resource. An AlreadyExists here (a racing writer created
			// it after our Get) is retriable via retriableWrite, so the retry
			// re-Gets and takes the update path below.
			created, createErr := p.createRuntimeConfig(ctx, req, runtimeConfig)
			if createErr != nil {
				return createErr
			}
			result = created
			return nil
		}

		// Update the existing resource
		updated, updateErr := p.updateRuntimeConfig(ctx, req, existing, runtimeConfig)
		if updateErr != nil {
			return updateErr
		}
		result = updated
		return nil
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		// retry.OnError reports a context cancellation as the last retriable
		// error, which is nil when none preceded it, so an interrupted write
		// would otherwise look like a success without a result.
		return nil, fmt.Errorf("creating or updating runtime config: %w", interruptedErr(ctx))
	}
	return result, nil
}

// runtimeConfigToWrite returns the HAProxyCfg the write starts from. A first
// attempt uses the informer's copy when it differs from desired, saving a read
// of the whole object: the update carries the cached resourceVersion, so a
// stale copy is refused with a conflict and the retry reads the live object.
// An unchanged object is confirmed by a live read, because skipping the write
// trusts it.
func (p *Publisher) runtimeConfigToWrite(ctx context.Context, namespace, name string, desired *haproxyv1alpha1.HAProxyCfg, firstAttempt bool) (*haproxyv1alpha1.HAProxyCfg, error) {
	if firstAttempt && p.listers != nil && p.listers.HAProxyCfgs != nil {
		cached, err := p.listers.HAProxyCfgs.HAProxyCfgs(namespace).Get(name)
		if err == nil && !runtimeConfigUpToDate(cached, desired) {
			return cached.DeepCopy(), nil
		}
	}
	return p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(namespace).Get(ctx, name, metav1.GetOptions{})
}

func runtimeConfigUpToDate(existing, desired *haproxyv1alpha1.HAProxyCfg) bool {
	return apiequality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, desired.Annotations) &&
		apiequality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		apiequality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences)
}

// interruptedErr names why a retry loop ended without a result.
func interruptedErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("interrupted")
}

// buildRuntimeConfig constructs a HAProxyCfg resource from the request.
func (p *Publisher) buildRuntimeConfig(name string, req *PublishRequest) *haproxyv1alpha1.HAProxyCfg {
	// Compress if content exceeds threshold
	result := p.compressIfNeeded(req.Config, req.CompressionThreshold, runtimeConfigKind)

	runtimeConfig := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: req.TemplateConfigNamespace,
			Annotations: map[string]string{
				AuxiliarySetIDAnnotationKey: req.auxiliarySetID,
			},
			Labels: map[string]string{
				"haproxy-haptic.org/template-config": req.TemplateConfigName,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         apiVersionV1Alpha1,
					Kind:               "HAProxyTemplateConfig",
					Name:               req.TemplateConfigName,
					UID:                req.TemplateConfigUID,
					Controller:         new(true),
					BlockOwnerDeletion: new(true),
				},
			},
		},
		Spec: haproxyv1alpha1.HAProxyCfgSpec{
			Path:       req.ConfigPath,
			Content:    result.content,
			Checksum:   req.Checksum, // Checksum is of original content
			Compressed: result.compressed,
		},
	}

	return runtimeConfig
}

// createRuntimeConfig creates a new HAProxyCfg resource.
func (p *Publisher) createRuntimeConfig(ctx context.Context, req *PublishRequest, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	created, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Create(ctx, runtimeConfig, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating runtime config: %w", err)
	}

	// Set validation error status if this is an invalid config
	if req.ValidationError != "" {
		if err := p.updateValidationErrorStatus(ctx, created, req.ValidationError); err != nil {
			return nil, err
		}
	}

	return created, nil
}

// updateValidationErrorStatus sets or clears the ValidationError on a HAProxyCfg status.
// Called only on validation error state transitions (ok→error or error→ok).
func (p *Publisher) updateValidationErrorStatus(ctx context.Context, cfg *haproxyv1alpha1.HAProxyCfg, validationError string) error {
	return p.patchRuntimeConfigStatusField(ctx, cfg, cfg, "validationError", validationError)
}

// updateRuntimeConfig updates an existing HAProxyCfg resource.
// Skips the update when the desired spec and ownership metadata are unchanged.
func (p *Publisher) updateRuntimeConfig(ctx context.Context, req *PublishRequest, existing, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	updated := existing
	if runtimeConfigUpToDate(existing, runtimeConfig) {
		p.logger.Debug("Skipping HAProxyCfg update, desired state unchanged",
			"name", existing.Name,
			"checksum", existing.Spec.Checksum,
		)
	} else {
		existing.Spec = runtimeConfig.Spec
		existing.Annotations = runtimeConfig.Annotations
		existing.Labels = runtimeConfig.Labels
		existing.OwnerReferences = runtimeConfig.OwnerReferences

		var err error
		updated, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("updating runtime config: %w", err)
		}
	}

	if updated.Status.ValidationError != req.ValidationError {
		if err := p.updateValidationErrorStatus(ctx, updated, req.ValidationError); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

// updateRuntimeConfigStatus updates the HAProxyCfg status with child resource references.
// Unchanged references still require the same publication identity.
func (p *Publisher) updateRuntimeConfigStatus(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, result *PublishResult) error {
	newAux := buildAuxiliaryFileReferences(
		runtimeConfig.Namespace,
		result,
		runtimeConfig.Annotations[AuxiliarySetIDAnnotationKey],
	)
	// The patch's test operations verify the publication identity on the
	// server, so a change needs no prior read of the whole object.
	if !auxiliaryRefsEqual(runtimeConfig.Status.AuxiliaryFiles, newAux) {
		return p.patchRuntimeConfigStatusField(ctx, runtimeConfig, runtimeConfig, "auxiliaryFiles", newAux)
	}

	current, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(runtimeConfig.Namespace).
		Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting runtime config: %w", err)
	}
	if err := validateRuntimePublication(runtimeConfig, current); err != nil {
		return err
	}

	if auxiliaryRefsEqual(current.Status.AuxiliaryFiles, newAux) {
		p.logger.Debug("Skipping HAProxyCfg status update, references unchanged",
			"name", current.Name,
		)
		return nil
	}

	return p.patchRuntimeConfigStatusField(ctx, runtimeConfig, current, "auxiliaryFiles", newAux)
}

// buildAuxiliaryFileReferences constructs an AuxiliaryFileReferences from a PublishResult.
func buildAuxiliaryFileReferences(namespace string, result *PublishResult, setID string) *haproxyv1alpha1.AuxiliaryFileReferences {
	// Returns nil (not []) for empty inputs so the AuxiliaryFileReferences
	// field stays absent in JSON via its omitempty tag — matching the prior
	// behavior of unexecuted appends leaving the field nil.
	refs := func(names []string, kind string) []haproxyv1alpha1.ResourceReference {
		if len(names) == 0 {
			return nil
		}
		out := make([]haproxyv1alpha1.ResourceReference, 0, len(names))
		for _, name := range names {
			out = append(out, haproxyv1alpha1.ResourceReference{
				Kind: kind, Name: name, Namespace: namespace,
			})
		}
		return out
	}
	return &haproxyv1alpha1.AuxiliaryFileReferences{
		SetID:           setID,
		MapFiles:        refs(result.MapFileNames, kindMapFile),
		SSLCertificates: refs(result.SecretNames, "Secret"),
		SSLCaFiles:      refs(result.SSLCaFileNames, "Secret"),
		GeneralFiles:    refs(result.GeneralFileNames, kindGeneralFile),
		CRTListFiles:    refs(result.CRTListFileNames, kindCRTListFile),
	}
}

// auxiliaryRefsEqual compares two AuxiliaryFileReferences for equality.
// ResourceReference is comparable (string-only fields), so slices.Equal is sufficient.
func auxiliaryRefsEqual(a, b *haproxyv1alpha1.AuxiliaryFileReferences) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return slices.Equal(a.MapFiles, b.MapFiles) &&
		a.SetID == b.SetID &&
		slices.Equal(a.SSLCertificates, b.SSLCertificates) &&
		slices.Equal(a.SSLCaFiles, b.SSLCaFiles) &&
		slices.Equal(a.GeneralFiles, b.GeneralFiles) &&
		slices.Equal(a.CRTListFiles, b.CRTListFiles)
}
