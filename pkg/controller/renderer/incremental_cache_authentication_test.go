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

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer/internal/queryidentity"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalComponentQueryCacheReauthenticatesCanonicalIdentity(t *testing.T) {
	component := incrementalComponent{name: "component"}
	newSession := func() *incrementalRenderSession {
		return &incrementalRenderSession{
			state: &incrementalRenderState{components: map[string]incrementalComponent{
				component.name: component,
				"other":        {name: "other"},
			}},
		}
	}
	session := newSession()
	key := session.registerComponentQuery(&component, "routes", "default", "route")
	gotComponent, gotSource, gotNamespace, gotName, resolved := session.resolveComponentQuery(key)
	require.True(t, resolved)
	require.Equal(t, component.name, gotComponent.name)
	require.Equal(t, "routes", gotSource)
	require.Equal(t, "default", gotNamespace)
	require.Equal(t, "route", gotName)

	copied := *session.componentQueries
	session.componentQueries = &copied
	gotComponent, gotSource, gotNamespace, gotName, resolved = session.resolveComponentQuery(key)
	require.False(t, resolved)
	requireZeroComponentQueryIdentity(t, &gotComponent, gotSource, gotNamespace, gotName)

	session = newSession()
	foreign := newSession()
	foreign.registerComponentQuery(&component, "routes", "default", "route")
	session.componentQueries = foreign.componentQueries
	gotComponent, gotSource, gotNamespace, gotName, resolved = session.resolveComponentQuery(key)
	require.False(t, resolved)
	requireZeroComponentQueryIdentity(t, &gotComponent, gotSource, gotNamespace, gotName)
}

func requireZeroComponentQueryIdentity(
	t *testing.T,
	component *incrementalComponent,
	source, namespace, name string,
) {
	t.Helper()
	require.Empty(t, component.name)
	require.Empty(t, source)
	require.Empty(t, namespace)
	require.Empty(t, name)
}

func TestIncrementalComponentQueryCacheAuthenticationDoesNotAllocate(t *testing.T) {
	tests := []struct {
		name                         string
		component, source, namespace string
		objectName                   string
	}{
		{name: "empty"},
		{name: "192 bytes", component: strings.Repeat("c", 192), source: "source"},
		{name: "193 bytes", component: strings.Repeat("c", 193), source: "source"},
		{name: "Kubernetes name", component: "component", objectName: strings.Repeat("n", 253)},
		{name: "multi kilobyte", component: "component", source: strings.Repeat("s", 8193), objectName: strings.Repeat("n", 4097)},
		{name: "Unicode and dots", component: "组件.component", source: "\x00\xff.source", namespace: "名前空間", objectName: "route.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := incrementalComponent{name: test.component}
			session := &incrementalRenderSession{
				state: &incrementalRenderState{components: map[string]incrementalComponent{
					component.name: component,
				}},
			}
			key := session.registerComponentQuery(&component, test.source, test.namespace, test.objectName)
			allocations := testing.AllocsPerRun(1000, func() {
				_, resolved := session.resolveQueryComponent(key)
				if !resolved {
					panic("component query did not resolve")
				}
			})
			require.Zero(t, allocations)
		})
	}
}

func TestComponentQueryKeyMatcherRejectsNoncanonicalKeys(t *testing.T) {
	component := incrementalComponent{name: "component"}
	key := componentQueryKey(&component, "source", "namespace", "name")
	require.True(t, componentQueryKeyMatches(key, &component, "source", "namespace", "name"))
	canonical := key.Opaque()
	tests := map[string]string{
		"truncated":          canonical[:len(canonical)-1],
		"extra":              canonical + "A",
		"extra empty frame":  canonical + "\x00",
		"overlong length":    overlongLastOpaqueFrame(canonical),
		"changed final byte": canonical[:len(canonical)-1] + "R",
	}
	for name, opaque := range tests {
		t.Run(name, func(t *testing.T) {
			require.False(t, componentQueryKeyMatches(
				incremental.NewQueryKey(opaque), &component, "source", "namespace", "name",
			))
		})
	}
	binaryName := string([]byte{0xfb, 0xff})
	binaryKey := componentQueryKey(&component, "source", "namespace", binaryName)
	require.True(t, componentQueryKeyMatches(
		binaryKey, &component, "source", "namespace", binaryName,
	))
	require.False(t, componentQueryKeyMatches(
		componentQueryKey(&component, "source", "namespace", binaryName+"\x00"),
		&component, "source", "namespace", binaryName,
	))
}

func BenchmarkComponentQueryAuthentication(b *testing.B) {
	for _, size := range []int{32, 8192} {
		part := strings.Repeat("x", size)
		component := incrementalComponent{name: part}
		key := componentQueryKey(&component, part, part, part)
		fields := queryidentity.Fields{Component: part, Source: part, Namespace: part, Name: part}
		owner := new(int)
		authority := queryidentity.NewAuthority(owner)
		require.True(b, authority.Register(owner, key, fields))

		b.Run(fmt.Sprintf("legacy/size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if !componentQueryKeyMatches(key, &component, part, part, part) {
					b.Fatal("component query key did not authenticate")
				}
			}
		})
		b.Run(fmt.Sprintf("root/size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, ok := authority.Lookup(owner, key); !ok {
					b.Fatal("component query root did not authenticate")
				}
			}
		})
	}
}

func TestAuthenticatedFreshComponentResultRejectsPoison(t *testing.T) {
	key := incremental.NewQueryKey("component")
	result := incrementalComponentResult{Text: "original"}
	newFresh := func(t *testing.T) (incremental.ExactValueRoot, *authenticatedFreshComponentResult) {
		t.Helper()
		return testFreshExactResult(t, key, &result)
	}

	tests := []struct {
		name   string
		poison func(*authenticatedFreshComponentResult)
	}{
		{name: "seal", poison: func(fresh *authenticatedFreshComponentResult) {
			copied := *fresh
			fresh.seal = &copied
		}},
		{name: "query provenance", poison: func(fresh *authenticatedFreshComponentResult) {
			fresh.key = incremental.NewQueryKey("other")
		}},
		{name: "authority", poison: func(fresh *authenticatedFreshComponentResult) {
			copied := *fresh.authority
			fresh.authority = &copied
		}},
		{name: "authoritative bytes", poison: func(fresh *authenticatedFreshComponentResult) {
			fresh.encoded = `{"text":"poison"}`
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, fresh := newFresh(t)
			test.poison(fresh)
			session := &incrementalRenderSession{
				freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{key: fresh},
			}

			_, err := session.authenticatedFreshResult(key, root)
			require.Error(t, err)
		})
	}
}
