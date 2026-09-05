// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import "gitlab.com/haproxy-haptic/haptic/pkg/templating"

type incrementalTemplateError struct {
	component string
	err       error
}

func (e *incrementalTemplateError) Error() string {
	switch err := e.err.(type) {
	case *templating.RenderError:
		return templating.NewRenderError(e.component, err.Cause).Error()
	case *templating.RenderTimeoutError:
		return (&templating.RenderTimeoutError{TemplateName: e.component, Cause: err.Cause}).Error()
	case *templating.TemplateNotFoundError:
		return templating.NewTemplateNotFoundError(e.component, err.AvailableTemplates).Error()
	default:
		return e.err.Error()
	}
}

func (e *incrementalTemplateError) Unwrap() error {
	return e.err
}

func remapIncrementalTemplateError(component, privateEntryPoint string, err error) error {
	if err == nil {
		return nil
	}
	switch typed := err.(type) {
	case *templating.RenderError:
		if typed.TemplateName == privateEntryPoint {
			return &incrementalTemplateError{component: component, err: err}
		}
	case *templating.RenderTimeoutError:
		if typed.TemplateName == privateEntryPoint {
			return &incrementalTemplateError{component: component, err: err}
		}
	case *templating.TemplateNotFoundError:
		if typed.TemplateName == privateEntryPoint {
			return &incrementalTemplateError{component: component, err: err}
		}
	}
	return err
}
