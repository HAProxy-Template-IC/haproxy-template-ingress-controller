// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package templating

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// scriggoCidrPartition splits a set of prefixes into the disjoint blocks each
// prefix is a union of, keyed by the input text: a longest-prefix lookup over
// all blocks then names exactly one block for any address, and a prefix's
// block list says which of them it covers. A bare address is a host prefix.
func scriggoCidrPartition(items any) (map[string][]string, error) {
	inputs := scriggoToStringSlice(items)
	prefixes := make([]netip.Prefix, 0, len(inputs))
	byInput := make(map[string]netip.Prefix, len(inputs))
	seen := make(map[netip.Prefix]bool, len(inputs))
	for _, raw := range inputs {
		prefix, err := parseCidrPrefix(raw)
		if err != nil {
			return nil, err
		}
		byInput[raw] = prefix
		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	result := make(map[string][]string, len(byInput))
	for raw, prefix := range byInput {
		blocks := cidrBlocks(prefix, prefixes)
		names := make([]string, len(blocks))
		for index, block := range blocks {
			names[index] = block.String()
		}
		slices.Sort(names)
		result[raw] = names
	}
	return result, nil
}

func parseCidrPrefix(raw string) (netip.Prefix, error) {
	text := strings.TrimSpace(raw)
	if !strings.Contains(text, "/") {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("cidr_partition: %w", err)
		}
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("cidr_partition: %w", err)
	}
	bits := prefix.Bits()
	addr := prefix.Addr()
	if addr.Is4In6() {
		bits -= 96
		if bits < 0 {
			return netip.Prefix{}, fmt.Errorf("cidr_partition: %q is not an IPv4 prefix", text)
		}
		addr = addr.Unmap()
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}

// cidrBlocks returns the leaves of prefix in the trie spanned by all prefixes.
func cidrBlocks(prefix netip.Prefix, all []netip.Prefix) []netip.Prefix {
	var inside []netip.Prefix
	for _, other := range all {
		if cidrStrictlyInside(other, prefix) {
			inside = append(inside, other)
		}
	}
	return splitCidr(prefix, inside)
}

func cidrStrictlyInside(inner, outer netip.Prefix) bool {
	return inner.Addr().Is4() == outer.Addr().Is4() &&
		inner.Bits() > outer.Bits() && outer.Contains(inner.Addr())
}

func splitCidr(prefix netip.Prefix, inside []netip.Prefix) []netip.Prefix {
	if len(inside) == 0 {
		return []netip.Prefix{prefix}
	}
	var blocks []netip.Prefix
	for _, half := range cidrHalves(prefix) {
		var within []netip.Prefix
		for _, other := range inside {
			if cidrStrictlyInside(other, half) {
				within = append(within, other)
			}
		}
		blocks = append(blocks, splitCidr(half, within)...)
	}
	return blocks
}

// cidrHalves returns the two prefixes one bit longer than prefix. The caller
// guarantees prefix is shorter than its address length.
func cidrHalves(prefix netip.Prefix) [2]netip.Prefix {
	bits := prefix.Bits()
	first := netip.PrefixFrom(prefix.Addr(), bits+1)
	var second netip.Addr
	if prefix.Addr().Is4() {
		raw := prefix.Addr().As4()
		raw[bits/8] |= 0x80 >> (bits % 8)
		second = netip.AddrFrom4(raw)
	} else {
		raw := prefix.Addr().As16()
		raw[bits/8] |= 0x80 >> (bits % 8)
		second = netip.AddrFrom16(raw)
	}
	return [2]netip.Prefix{first, netip.PrefixFrom(second, bits+1)}
}
