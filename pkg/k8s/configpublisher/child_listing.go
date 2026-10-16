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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// auxiliaryChildKinds is every kind a publication writes children of.
var auxiliaryChildKinds = []string{kindMapFile, kindSecret, kindGeneralFile, kindCRTListFile}

type listedChild struct {
	obj    metav1.Object
	get    func(context.Context, string) (metav1.Object, error)
	remove func(context.Context, string, metav1.DeleteOptions) error
}

// childListing holds one label-selected LIST per auxiliary kind. Its LISTs set
// no resourceVersion, so each is as current as one GET per child at that time.
type childListing struct {
	namespace string
	selector  string
	byKind    map[string][]listedChild
	byKey     map[string]metav1.Object
}

func newChildListing(namespace, selector string) *childListing {
	return &childListing{namespace: namespace, selector: selector, byKind: map[string][]listedChild{}, byKey: map[string]metav1.Object{}}
}

// listInto lists the kinds the listing does not cover yet.
func (p *Publisher) listInto(ctx context.Context, listing *childListing, kinds []string) error {
	for _, kind := range kinds {
		if _, listed := listing.byKind[kind]; listed {
			continue
		}
		children, err := p.listChildKind(ctx, listing.namespace, kind, listing.selector)
		if err != nil {
			return fmt.Errorf("listing %s children: %w", kind, err)
		}
		listing.byKind[kind] = children
		for _, child := range children {
			listing.byKey[kind+"/"+child.obj.GetName()] = child.obj
		}
	}
	return nil
}

// lookup reports covered=false when the kind was never listed.
func (l *childListing) lookup(kind, name string) (obj metav1.Object, covered bool) {
	if l == nil {
		return nil, false
	}
	if _, listed := l.byKind[kind]; !listed {
		return nil, false
	}
	return l.byKey[kind+"/"+name], true
}

func (p *Publisher) listChildKind(ctx context.Context, namespace, kind, selector string) ([]listedChild, error) {
	crd := p.crdClient.HaproxyTemplateICV1alpha1()
	options := metav1.ListOptions{LabelSelector: selector}
	switch kind {
	case kindMapFile:
		client := crd.HAProxyMapFiles(namespace)
		items, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		return childrenOf(items.Items, objectGetter(client.Get), client.Delete), nil
	case kindGeneralFile:
		client := crd.HAProxyGeneralFiles(namespace)
		items, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		return childrenOf(items.Items, objectGetter(client.Get), client.Delete), nil
	case kindCRTListFile:
		client := crd.HAProxyCRTListFiles(namespace)
		items, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		return childrenOf(items.Items, objectGetter(client.Get), client.Delete), nil
	case kindSecret:
		client := p.k8sClient.CoreV1().Secrets(namespace)
		items, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		return childrenOf(items.Items, objectGetter(client.Get), client.Delete), nil
	default:
		return nil, fmt.Errorf("unsupported auxiliary kind %q", kind)
	}
}

func childrenOf[E any, P interface {
	*E
	metav1.Object
}](items []E, get func(context.Context, string) (metav1.Object, error), remove func(context.Context, string, metav1.DeleteOptions) error) []listedChild {
	children := make([]listedChild, len(items))
	for i := range items {
		children[i] = listedChild{obj: P(&items[i]), get: get, remove: remove}
	}
	return children
}

func objectGetter[T metav1.Object](get func(context.Context, string, metav1.GetOptions) (T, error)) func(context.Context, string) (metav1.Object, error) {
	return func(ctx context.Context, name string) (metav1.Object, error) {
		item, err := get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return item, nil
	}
}

// listedOrGet reads a child from the listing; children it lacks, such as those
// owned by checkpoints published before sharing, are read live.
func listedOrGet[T metav1.Object](ctx context.Context, listing *childListing, kind, name string, get func(context.Context, string, metav1.GetOptions) (T, error)) (T, error) {
	if obj, _ := listing.lookup(kind, name); obj != nil {
		if item, ok := obj.(T); ok {
			return item, nil
		}
	}
	return get(ctx, name, metav1.GetOptions{})
}

// listedFirst answers a publication's first read of a child from the listing
// taken at its start; a child absent from a listed kind did not exist then.
// Retries read live, so a racing writer is seen as with a GET.
func listedFirst[T metav1.Object](listing *childListing, kind, name string, get func(context.Context, string, metav1.GetOptions) (T, error)) func(context.Context) (T, error) {
	obj, first := listing.lookup(kind, name)
	return func(ctx context.Context) (T, error) {
		if !first {
			return get(ctx, name, metav1.GetOptions{})
		}
		first = false
		if item, ok := obj.(T); ok {
			return item, nil
		}
		var absent T
		return absent, apierrors.NewNotFound(schema.GroupResource{Resource: kind}, name)
	}
}
