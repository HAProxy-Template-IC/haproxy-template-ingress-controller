package comparator

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareACLs compares ACL configurations within a frontend or backend.
//
// HAProxy ACLs are positional: a single ACL NAME may appear on multiple lines
// in the rendered config, each with its own (criterion, value) pair. HAProxy
// OR-combines lines that share a name — so two `acl X req_ssl_sni -m str foo`
// + `acl X req_ssl_sni -m end .bar` lines together mean "ACL X is true when
// SNI is `foo` OR ends with `.bar`". The DataPlane API addresses ACLs by
// index within their parent and preserves this OR semantics; both lines must
// be created as separate index entries.
//
// Earlier this function indexed ACLs into `map[string]int` by ACLName and
// emitted at most one operation per name. When the chart emitted two ACL
// lines with the same name, the second silently overwrote the first in the
// index, so only one create operation reached the DataPlane API. Reject
// rules that depended on the OR-combined truth then misfired (the rendered
// haproxy.cfg had the wildcard SNI in the route_sni ACL; the dataplane
// on-disk haproxy.cfg only had the exact-match line; SNIs that should have
// satisfied `route_sni` failed the `!route_sni` test and triggered a
// `tcp-request content reject`).
//
// Same fix as every other indexed-child rule (HTTP/TCP request rules, TCP
// response rules, filters, …): use compareEditedItems for LCS-based
// positional diffing. ACLs already have a content-Equal method on
// `*models.ACL` so the pattern slots in unchanged.
func (c *Comparator) compareACLs(parentType, parentName string, currentACLs, desiredACLs models.Acls, _ *DiffSummary) []Operation {
	create, remove, update := sections.ACLBackendOps.Create, sections.ACLBackendOps.Delete, sections.ACLBackendOps.Update
	if parentType == parentTypeFrontend {
		create, remove, update = sections.ACLFrontendOps.Create, sections.ACLFrontendOps.Delete, sections.ACLFrontendOps.Update
	}
	return compareEditedItems(
		currentACLs, desiredACLs,
		func(a, b *models.ACL) bool { return a.Equal(*b) },
		func(acl *models.ACL, i int) Operation { return create(parentName, acl, i) },
		func(acl *models.ACL, i int) Operation { return remove(parentName, acl, i) },
		func(acl *models.ACL, i int) Operation { return update(parentName, acl, i) },
	)
}

// compareEditedItems runs an LCS-based diff (diffIndexedRules +
// collapseEdits) over current/desired and emits create/delete/update
// operations for the resulting edit script. Updates use the new value at the
// position of the old item, matching what collapseEdits already pairs up.
//
// EMISSION ORDER: updates first (no shift), then deletes in DESCENDING
// OldIndex order, then inserts in ASCENDING NewIndex order. The order is
// load-bearing under sequential application against the dataplane API's
// underlying config-parser, which:
//
//   - Set(idx) replaces in place — no shift.
//   - Delete(idx) shifts every element after idx down one slot
//     (see haproxytech/client-native config-parser/parsers/http/http-request_generated.go's
//     `(*Requests).Delete`).
//   - Insert(idx) shifts every element at or after idx up one slot.
//
// Myers emits the edit script in OLD-index forward order, which means
// ascending-OldIndex deletes interleaved with inserts. Applied sequentially,
// each Delete(N) cascade-shifts the rest, so a subsequent Delete(N+1) targets
// a different rule than the comparator intended. Equally, an Insert applied
// BEFORE a later Update/Delete shifts the target rule's position, so the
// OldIndex on that Update/Delete no longer points at the rule the comparator
// picked. Reordering to (updates → deletes-descending → inserts-ascending)
// gives every op an index that resolves to the right rule in the staged
// state:
//
//  1. Updates first: OldIndex matches OLD's positions exactly.
//  2. Deletes descending: each Delete only ever shifts indices higher than
//     itself, but we've already processed those (or they were deletes too
//     and are now gone), so lower OldIndex values still resolve correctly.
//  3. Inserts ascending: by now the staged state is OLD minus the deletes;
//     ascending NewIndex inserts each new rule at its final-list position
//     (the next-higher insert shifts further-right items, which is fine).
//
// Same root cause as the compareIndexedItems descending-delete fix in
// compare_features.go — both functions previously emitted ascending deletes
// that wipe the wrong rules when applied sequentially against the live index
// layout.
// Symptom in the e2e suite: a redirect rule from an Ingress with
// nginx.ingress.kubernetes.io/{temporal,permanent}-redirect would briefly
// land in HAProxy then disappear during a sibling-test cleanup reconcile
// (chart re-render kept the rule, comparator's diff against the new
// position layout emitted ascending-OldIndex deletes that wiped the
// wrong rules).
func compareEditedItems[T any](
	current, desired []T,
	equal func(T, T) bool,
	create func(item T, index int) Operation,
	remove func(item T, index int) Operation,
	update func(item T, index int) Operation,
) []Operation {
	diffs := diffIndexedRules(current, desired, equal)
	edits := collapseEdits(diffs)

	var updates, deletes, inserts []Operation
	for _, e := range edits {
		switch e.Op {
		case editInsert:
			inserts = append(inserts, create(e.New, e.NewIndex))
		case editDelete:
			deletes = append(deletes, remove(e.Old, e.OldIndex))
		case editUpdate:
			updates = append(updates, update(e.New, e.OldIndex))
		}
	}

	// collapseEdits emits deletes and inserts in ascending OldIndex /
	// NewIndex respectively (Myers' forward order). Inserts are fine as-is;
	// deletes need reversing.
	out := make([]Operation, 0, len(updates)+len(deletes)+len(inserts))
	out = append(out, updates...)
	for j := len(deletes) - 1; j >= 0; j-- {
		out = append(out, deletes[j])
	}
	out = append(out, inserts...)
	return out
}

// compareHTTPRequestRules compares HTTP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations
// instead of cascading UPDATEs caused by index shifts.
func (c *Comparator) compareHTTPRequestRules(parentType, parentName string, currentRules, desiredRules models.HTTPRequestRules) []Operation {
	create, remove, update := sections.HTTPRequestRuleBackendOps.Create, sections.HTTPRequestRuleBackendOps.Delete, sections.HTTPRequestRuleBackendOps.Update
	if parentType == parentTypeFrontend {
		create, remove, update = sections.HTTPRequestRuleFrontendOps.Create, sections.HTTPRequestRuleFrontendOps.Delete, sections.HTTPRequestRuleFrontendOps.Update
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPRequestRule) bool { return a.Equal(*b) },
		func(r *models.HTTPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareHTTPResponseRules compares HTTP response rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPResponseRules(parentType, parentName string, currentRules, desiredRules models.HTTPResponseRules) []Operation {
	create, remove, update := sections.HTTPResponseRuleBackendOps.Create, sections.HTTPResponseRuleBackendOps.Delete, sections.HTTPResponseRuleBackendOps.Update
	if parentType == parentTypeFrontend {
		create, remove, update = sections.HTTPResponseRuleFrontendOps.Create, sections.HTTPResponseRuleFrontendOps.Delete, sections.HTTPResponseRuleFrontendOps.Update
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPResponseRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.HTTPResponseRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPRequestRules compares TCP request rule configurations within a frontend or backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPRequestRules(parentType, parentName string, currentRules, desiredRules models.TCPRequestRules) []Operation {
	create, remove, update := sections.TCPRequestRuleBackendOps.Create, sections.TCPRequestRuleBackendOps.Delete, sections.TCPRequestRuleBackendOps.Update
	if parentType == parentTypeFrontend {
		create, remove, update = sections.TCPRequestRuleFrontendOps.Create, sections.TCPRequestRuleFrontendOps.Delete, sections.TCPRequestRuleFrontendOps.Update
	}
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPRequestRule) bool { return a.Equal(*b) },
		func(r *models.TCPRequestRule, i int) Operation { return create(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return remove(parentName, r, i) },
		func(r *models.TCPRequestRule, i int) Operation { return update(parentName, r, i) },
	)
}

// compareTCPResponseRules compares TCP response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareTCPResponseRules(parentName string, currentRules, desiredRules models.TCPResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.TCPResponseRule) bool { return a.Equal(*b) },
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.TCPResponseRuleBackendOps.Create(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.TCPResponseRuleBackendOps.Delete(parentName, r, i)
		},
		func(r *models.TCPResponseRule, i int) Operation {
			return sections.TCPResponseRuleBackendOps.Update(parentName, r, i)
		},
	)
}

// compareStickRules compares stick rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareStickRules(backendName string, currentRules, desiredRules models.StickRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.StickRule) bool { return a.Equal(*b) },
		func(r *models.StickRule, i int) Operation {
			return sections.StickRuleBackendOps.Create(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.StickRuleBackendOps.Delete(backendName, r, i)
		},
		func(r *models.StickRule, i int) Operation {
			return sections.StickRuleBackendOps.Update(backendName, r, i)
		},
	)
}

// compareHTTPAfterResponseRules compares HTTP after response rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareHTTPAfterResponseRules(backendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPAfterResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleBackendOps.Create(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleBackendOps.Delete(backendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleBackendOps.Update(backendName, r, i)
		},
	)
}

// compareFrontendHTTPAfterResponseRules compares HTTP after response rule configurations within a frontend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
//
// The frontend variant exists because chart libraries emit `http-after-response`
// directives in frontends for SPOA-driven auth-failure header forwarding (e.g.
// haproxy-ingress.github.io/auth-headers-fail surfaces WWW-Authenticate /
// X-Error-Reason on 401 responses generated by `http-request deny`, which
// `http-response` cannot mutate). Without this comparator path the
// directives render but never reach HAProxy via the dataplane API.
func (c *Comparator) compareFrontendHTTPAfterResponseRules(frontendName string, currentRules, desiredRules models.HTTPAfterResponseRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.HTTPAfterResponseRule) bool { return a.Equal(*b) },
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleFrontendOps.Create(frontendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleFrontendOps.Delete(frontendName, r, i)
		},
		func(r *models.HTTPAfterResponseRule, i int) Operation {
			return sections.HTTPAfterResponseRuleFrontendOps.Update(frontendName, r, i)
		},
	)
}

// compareBackendSwitchingRules compares backend switching rule configurations within a frontend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareBackendSwitchingRules(frontendName string, currentRules, desiredRules models.BackendSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.BackendSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.BackendSwitchingRuleFrontendOps.Create(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.BackendSwitchingRuleFrontendOps.Delete(frontendName, r, i)
		},
		func(r *models.BackendSwitchingRule, i int) Operation {
			return sections.BackendSwitchingRuleFrontendOps.Update(frontendName, r, i)
		},
	)
}

// compareServerSwitchingRules compares server switching rule configurations within a backend.
// Uses LCS-based content matching to produce minimal INSERT/DELETE/UPDATE operations.
func (c *Comparator) compareServerSwitchingRules(backendName string, currentRules, desiredRules models.ServerSwitchingRules) []Operation {
	return compareEditedItems(
		currentRules, desiredRules,
		func(a, b *models.ServerSwitchingRule) bool { return a.Equal(*b) },
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.ServerSwitchingRuleBackendOps.Create(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.ServerSwitchingRuleBackendOps.Delete(backendName, r, i)
		},
		func(r *models.ServerSwitchingRule, i int) Operation {
			return sections.ServerSwitchingRuleBackendOps.Update(backendName, r, i)
		},
	)
}
