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
	"io"
	"net"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// PublicationStage identifies the part of a config publication that failed.
type PublicationStage string

const (
	PublicationStageRuntimeConfig PublicationStage = "runtime-config"
	PublicationStageAuxiliary     PublicationStage = "auxiliary-resource"
	PublicationStageReferences    PublicationStage = "auxiliary-references"
	PublicationStageCleanup       PublicationStage = "auxiliary-cleanup"
)

// IncompletePublicationError reports which required resource was not published.
type IncompletePublicationError struct {
	Stage                  PublicationStage
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	ResourceKind           string
	ResourceName           string
	Err                    error
}

func (e *IncompletePublicationError) Error() string {
	return fmt.Sprintf("publishing %s %s/%s during %s: %v",
		e.ResourceKind, e.RuntimeConfigNamespace, e.ResourceName, e.Stage, e.Err)
}

func (e *IncompletePublicationError) Unwrap() error {
	return e.Err
}

// IsRetryablePublicationError reports whether repeating the same request can
// recover without an input change.
func IsRetryablePublicationError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) ||
		apierrors.IsNotFound(err) || apierrors.IsGone(err) || apierrors.IsResourceExpired(err) ||
		apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) || apierrors.IsUnexpectedServerError(err) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func incompletePublicationError(
	stage PublicationStage,
	runtimeConfigNamespace, runtimeConfigName, resourceKind, resourceName string,
	err error,
) *IncompletePublicationError {
	return &IncompletePublicationError{
		Stage:                  stage,
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		ResourceKind:           resourceKind,
		ResourceName:           resourceName,
		Err:                    err,
	}
}
