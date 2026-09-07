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

package renderplan

import (
	"errors"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

// ErrDocumentTransitionRequiresRebuild reports a transition the sparse delta protocol cannot encode.
var ErrDocumentTransitionRequiresRebuild = errors.New("render plan document transition requires a full rebuild")

// ReconcileSnapshotWithConfigDocument owns exact plan metadata while retaining config as a document.
func ReconcileSnapshotWithConfigDocument(
	authority *Authority,
	previous *Snapshot,
	plan *Plan,
	document rendercontent.Document,
) (*Snapshot, *Delta, error) {
	if err := authority.ValidateAuthentication(); err != nil {
		return nil, nil, err
	}
	if previous != nil {
		if err := authority.ValidateSnapshot(previous); err != nil {
			return nil, nil, err
		}
	}
	configIndex, err := validateDocumentPlanSource(authority, previous, plan, document)
	if err != nil {
		return nil, nil, err
	}
	if previous == nil {
		snapshot, buildErr := buildInitialDocumentPlanSnapshot(authority, plan, document, configIndex)
		return snapshot, nil, buildErr
	}
	if previous.root.schema != plan.SchemaVersion {
		return nil, nil, ErrDocumentTransitionRequiresRebuild
	}

	changes, err := reconcileDocumentPlanChanges(authority, previous, plan, document, configIndex)
	if err != nil {
		return nil, nil, err
	}
	next, err := applyDocumentPlanChanges(authority, previous, plan, changes)
	if err != nil {
		return nil, nil, err
	}
	structural := documentPlanChangesStructural(
		changes.sections, changes.backends, changes.profiles,
		changes.maps, changes.crtLists, changes.files,
	)
	delta := sealPlanDelta(
		authority, previous, next, changes.sections, changes.backends, changes.profiles,
		changes.maps, changes.crtLists, changes.files, structural,
	)
	if err := delta.ValidateAuthentication(); err != nil {
		return nil, nil, err
	}
	return next, delta, nil
}

type documentPlanChanges struct {
	sections []*sealedSequenceChange[Section]
	backends []*sealedMapChange[Backend]
	profiles []*sealedMapChange[Profile]
	maps     []*sealedMapChange[Map]
	crtLists []*sealedMapChange[CRTList]
	files    []*sealedSequenceChange[File]
}

func reconcileDocumentPlanChanges(
	authority *Authority,
	previous *Snapshot,
	plan *Plan,
	document rendercontent.Document,
	configIndex int,
) (*documentPlanChanges, error) {
	sections, err := reconcileSequenceCollection(
		authority, previous.root.sections, sectionSnapshotCollection,
		plan.Sections, ownSection, exactSection,
	)
	if err != nil {
		return nil, err
	}
	backends, err := reconcileMapCollection(
		authority, previous.root.backends, backendSnapshotCollection,
		plan.Backends, ownBackend, exactBackend,
	)
	if err != nil {
		return nil, err
	}
	profiles, err := reconcileMapCollection(
		authority, previous.root.profiles, profileSnapshotCollection,
		plan.Profiles, ownProfile, exactProfile,
	)
	if err != nil {
		return nil, err
	}
	mapsChanges, err := reconcileMapCollection(
		authority, previous.root.maps, mapSnapshotCollection,
		plan.Maps, ownMap, exactMap,
	)
	if err != nil {
		return nil, err
	}
	crtLists, err := reconcileMapCollection(
		authority, previous.root.crtLists, crtListSnapshotCollection,
		plan.CRTLists, ownCRTList, exactCRTList,
	)
	if err != nil {
		return nil, err
	}
	files, err := reconcileDocumentFileCollection(
		authority, previous.root.files, plan.Files, document, configIndex,
	)
	if err != nil {
		return nil, err
	}
	return &documentPlanChanges{
		sections: sections, backends: backends, profiles: profiles,
		maps: mapsChanges, crtLists: crtLists, files: files,
	}, nil
}

func applyDocumentPlanChanges(
	authority *Authority,
	previous *Snapshot,
	plan *Plan,
	changes *documentPlanChanges,
) (*Snapshot, error) {
	nextSections, err := applySequenceChanges(
		authority, previous.root.sections, sectionSnapshotCollection, changes.sections,
	)
	if err != nil {
		return nil, err
	}
	nextBackends, err := applyMapChanges(
		authority, previous.root.backends, backendSnapshotCollection, changes.backends,
	)
	if err != nil {
		return nil, err
	}
	nextProfiles, err := applyMapChanges(
		authority, previous.root.profiles, profileSnapshotCollection, changes.profiles,
	)
	if err != nil {
		return nil, err
	}
	nextMaps, err := applyMapChanges(
		authority, previous.root.maps, mapSnapshotCollection, changes.maps,
	)
	if err != nil {
		return nil, err
	}
	nextCRTLists, err := applyMapChanges(
		authority, previous.root.crtLists, crtListSnapshotCollection, changes.crtLists,
	)
	if err != nil {
		return nil, err
	}
	nextFiles, err := applySequenceChanges(
		authority, previous.root.files, fileSnapshotCollection, changes.files,
	)
	if err != nil {
		return nil, err
	}
	if nextSections == previous.root.sections && nextBackends == previous.root.backends &&
		nextProfiles == previous.root.profiles && nextMaps == previous.root.maps &&
		nextCRTLists == previous.root.crtLists && nextFiles == previous.root.files {
		return previous, nil
	}
	root := sealDeferredPlanRoot(
		authority, plan.SchemaVersion, nextSections, nextBackends,
		nextProfiles, nextMaps, nextCRTLists, nextFiles,
	)
	return sealSnapshot(authority, root), nil
}

// SectionsCopy returns detached ordered section metadata without materializing config.
func (s *Snapshot) SectionsCopy() ([]Section, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return materializeSnapshotSequence(s.root.sections, ownSection)
}

func validateDocumentPlanSource(
	authority *Authority,
	previous *Snapshot,
	plan *Plan,
	document rendercontent.Document,
) (int, error) {
	if plan == nil {
		return 0, errNilSnapshotPlan
	}
	if err := document.ValidateAuthentication(); err != nil {
		return 0, errors.Join(errInexactSnapshotPlan, err)
	}
	if plan.ID != "" {
		return 0, errInexactSnapshotPlan
	}
	documentBytes, err := document.Bytes()
	if err != nil {
		return 0, err
	}
	verified, err := verifiedSnapshotSections(authority, previous)
	if err != nil {
		return 0, err
	}
	if err := validateDocumentPlanSections(plan, documentBytes, verified); err != nil {
		return 0, err
	}
	for name := range plan.Backends {
		if !plan.Backends[name].ContentKnown {
			return 0, errInexactSnapshotPlan
		}
	}
	return validateDocumentPlanFiles(plan, documentBytes)
}

type sectionIdentity struct {
	kind string
	name string
}

// verifiedSnapshotSections indexes the sections of a sealed snapshot, whose
// digests were checked when it was sealed, so a plan that carries them over
// does not hash the whole configuration again.
func verifiedSnapshotSections(authority *Authority, previous *Snapshot) (map[sectionIdentity]Section, error) {
	if previous == nil {
		return map[sectionIdentity]Section{}, nil
	}
	entries, err := snapshotEntries(previous.root.sections, authority, sectionSnapshotCollection)
	if err != nil {
		return nil, err
	}
	verified := make(map[sectionIdentity]Section, len(entries))
	for _, entry := range entries {
		section := entry.value.value
		verified[sectionIdentity{kind: section.Kind, name: section.Name}] = section
	}
	return verified, nil
}

func validateDocumentPlanSections(plan *Plan, documentBytes int, verified map[sectionIdentity]Section) error {
	sectionBytes := 0
	for index := range plan.Sections {
		section := plan.Sections[index]
		if !section.TextKnown || section.Length != len(section.Text) || section.Length < 0 ||
			sectionBytes > documentBytes-section.Length {
			return errInexactSnapshotPlan
		}
		// A string compare is a pointer compare when the text was carried
		// over from the sealed snapshot, which is how an unchanged section
		// arrives here.
		known, carried := verified[sectionIdentity{kind: section.Kind, name: section.Name}]
		if !carried || known.TextDigest != section.TextDigest || known.Text != section.Text {
			if section.TextDigest != DigestString(section.Text) {
				return errInexactSnapshotPlan
			}
		}
		sectionBytes += section.Length
	}
	if sectionBytes != documentBytes {
		return errInexactSnapshotPlan
	}
	return nil
}

func validateDocumentPlanFiles(plan *Plan, documentBytes int) (int, error) {
	configIndex := -1
	for index := range plan.Files {
		file := plan.Files[index]
		if file.Kind == FileKindConfig {
			if configIndex != -1 || file.Path != ConfigFilePath || !file.ReloadOnChange ||
				file.ContentKnown || file.Content != "" || file.Digest != "" ||
				file.Size != int64(documentBytes) {
				return 0, errInexactSnapshotPlan
			}
			configIndex = index
			continue
		}
		if !file.ContentKnown || file.Size != int64(len(file.Content)) ||
			file.Digest != DigestString(file.Content) {
			return 0, errInexactSnapshotPlan
		}
	}
	if configIndex == -1 {
		return 0, errInexactSnapshotPlan
	}
	return configIndex, nil
}

func buildInitialDocumentPlanSnapshot(
	authority *Authority,
	plan *Plan,
	document rendercontent.Document,
	configIndex int,
) (*Snapshot, error) {
	sections, err := buildSnapshotSequence(
		authority, sectionSnapshotCollection, plan.Sections, nil, ownSection, exactSection,
	)
	if err != nil {
		return nil, err
	}
	backends, err := buildSnapshotMap(
		authority, backendSnapshotCollection, plan.Backends, nil, ownBackend, exactBackend,
	)
	if err != nil {
		return nil, err
	}
	profiles, err := buildSnapshotMap(
		authority, profileSnapshotCollection, plan.Profiles, nil, ownProfile, exactProfile,
	)
	if err != nil {
		return nil, err
	}
	mapsCollection, err := buildSnapshotMap(
		authority, mapSnapshotCollection, plan.Maps, nil, ownMap, exactMap,
	)
	if err != nil {
		return nil, err
	}
	crtLists, err := buildSnapshotMap(
		authority, crtListSnapshotCollection, plan.CRTLists, nil, ownCRTList, exactCRTList,
	)
	if err != nil {
		return nil, err
	}
	files, err := buildInitialDocumentFileCollection(
		authority, plan.Files, document, configIndex,
	)
	if err != nil {
		return nil, err
	}
	root := sealDeferredPlanRoot(
		authority, plan.SchemaVersion, sections, backends, profiles,
		mapsCollection, crtLists, files,
	)
	return sealSnapshot(authority, root), nil
}

func buildInitialDocumentFileCollection(
	authority *Authority,
	files []File,
	document rendercontent.Document,
	configIndex int,
) (*snapshotCollection[File], error) {
	entries := make([]*snapshotEntry[File], len(files))
	for index := range files {
		if index == configIndex {
			entry, err := sealSnapshotDocumentFileEntry(
				authority, snapshotKey{index: index}, &files[index], document, false,
			)
			if err != nil {
				return nil, err
			}
			entries[index] = entry
			continue
		}
		entries[index] = sealSnapshotEntry(
			authority, fileSnapshotCollection, snapshotKey{index: index}, ownFile(files[index]),
		)
	}
	return buildSnapshotCollection(
		authority, fileSnapshotCollection, files != nil, entries, nil,
	)
}

func reconcileSequenceCollection[T any](
	authority *Authority,
	base *snapshotCollection[T],
	kind snapshotCollectionKind,
	source []T,
	own func(T) T,
	equal func(T, T) bool,
) ([]*sealedSequenceChange[T], error) {
	if base.present != (source != nil) {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	entries, err := snapshotEntries(base, authority, kind)
	if err != nil {
		return nil, err
	}
	prefix, suffix := exactSequenceEdges(entries, source, equal)
	baseMiddle := len(entries) - prefix - suffix
	sourceMiddle := len(source) - prefix - suffix
	if difference := baseMiddle - sourceMiddle; difference < -1 || difference > 1 {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	paired := min(baseMiddle, sourceMiddle)
	changes := make([]*sealedSequenceChange[T], 0, max(baseMiddle, sourceMiddle))
	for offset := range paired {
		index := prefix + offset
		if equal(entries[index].value.value, source[index]) {
			continue
		}
		changes = append(changes, sealSequenceChange(
			sequenceReplaceChange, index, entries[index],
			sealSnapshotEntry(authority, kind, snapshotKey{}, own(source[index])),
		))
	}
	switch {
	case sourceMiddle > baseMiddle:
		index := prefix + paired
		changes = append(changes, sealSequenceChange(
			sequenceInsertChange, index, nil,
			sealSnapshotEntry(authority, kind, snapshotKey{}, own(source[index])),
		))
	case baseMiddle > sourceMiddle:
		index := prefix + paired
		changes = append(changes, sealSequenceChange(
			sequenceDeleteChange, index, entries[index], nil,
		))
	}
	return changes, nil
}

func exactSequenceEdges[T any](
	base []*snapshotEntry[T],
	source []T,
	equal func(T, T) bool,
) (prefix, suffix int) {
	for prefix < len(base) && prefix < len(source) && equal(base[prefix].value.value, source[prefix]) {
		prefix++
	}
	for suffix < len(base)-prefix && suffix < len(source)-prefix &&
		equal(base[len(base)-1-suffix].value.value, source[len(source)-1-suffix]) {
		suffix++
	}
	return prefix, suffix
}

func reconcileMapCollection[T any](
	authority *Authority,
	base *snapshotCollection[T],
	kind snapshotCollectionKind,
	source map[string]T,
	own func(T) T,
	equal func(T, T) bool,
) ([]*sealedMapChange[T], error) {
	if base.present != (source != nil) {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	baseEntries, err := snapshotEntries(base, authority, kind)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*snapshotEntry[T], len(baseEntries))
	keys := make([]string, 0, len(baseEntries)+len(source))
	for _, entry := range baseEntries {
		byName[entry.key.name] = entry
		keys = append(keys, entry.key.name)
	}
	for name := range source {
		if _, exists := byName[name]; !exists {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	changes := make([]*sealedMapChange[T], 0)
	for _, name := range keys {
		before := byName[name]
		value, present := source[name]
		if before != nil && present && equal(before.value.value, value) {
			continue
		}
		var after *snapshotEntry[T]
		if present {
			after = sealSnapshotEntry(
				authority, kind, snapshotKey{index: -1, name: name}, own(value),
			)
			if kind == mapSnapshotCollection {
				after.predecessor = before
			}
		}
		changes = append(changes, sealMapChange(name, before, after))
	}
	return changes, nil
}

func reconcileDocumentFileCollection(
	authority *Authority,
	base *snapshotCollection[File],
	source []File,
	document rendercontent.Document,
	configIndex int,
) ([]*sealedSequenceChange[File], error) {
	if base.present != (source != nil) {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	entries, err := snapshotEntries(base, authority, fileSnapshotCollection)
	if err != nil {
		return nil, err
	}
	prefix, suffix, err := exactDocumentFileEdges(entries, source, document, configIndex)
	if err != nil {
		return nil, err
	}
	baseMiddle := len(entries) - prefix - suffix
	sourceMiddle := len(source) - prefix - suffix
	if difference := baseMiddle - sourceMiddle; difference < -1 || difference > 1 {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	paired := min(baseMiddle, sourceMiddle)
	changes := make([]*sealedSequenceChange[File], 0, max(baseMiddle, sourceMiddle))
	for offset := range paired {
		index := prefix + offset
		exact, compareErr := exactDocumentFileAt(
			entries[index], &source[index], index == configIndex, document,
		)
		if compareErr != nil {
			return nil, compareErr
		}
		if exact {
			continue
		}
		after, buildErr := documentFileEntry(
			authority, &source[index], index == configIndex, document,
		)
		if buildErr != nil {
			return nil, buildErr
		}
		changes = append(changes, sealSequenceChange(
			sequenceReplaceChange, index, entries[index], after,
		))
	}
	switch {
	case sourceMiddle > baseMiddle:
		index := prefix + paired
		after, buildErr := documentFileEntry(
			authority, &source[index], index == configIndex, document,
		)
		if buildErr != nil {
			return nil, buildErr
		}
		changes = append(changes, sealSequenceChange(
			sequenceInsertChange, index, nil, after,
		))
	case baseMiddle > sourceMiddle:
		index := prefix + paired
		changes = append(changes, sealSequenceChange(
			sequenceDeleteChange, index, entries[index], nil,
		))
	}
	return changes, nil
}

func exactDocumentFileEdges(
	entries []*snapshotEntry[File],
	source []File,
	document rendercontent.Document,
	configIndex int,
) (prefix, suffix int, err error) {
	for prefix < len(entries) && prefix < len(source) {
		exact, compareErr := exactDocumentFileAt(
			entries[prefix], &source[prefix], prefix == configIndex, document,
		)
		if compareErr != nil {
			return 0, 0, compareErr
		}
		if !exact {
			break
		}
		prefix++
	}
	for suffix < len(entries)-prefix && suffix < len(source)-prefix {
		baseIndex := len(entries) - 1 - suffix
		sourceIndex := len(source) - 1 - suffix
		exact, compareErr := exactDocumentFileAt(
			entries[baseIndex], &source[sourceIndex], sourceIndex == configIndex, document,
		)
		if compareErr != nil {
			return 0, 0, compareErr
		}
		if !exact {
			break
		}
		suffix++
	}
	return prefix, suffix, nil
}

func exactDocumentFileAt(
	entry *snapshotEntry[File],
	source *File,
	config bool,
	document rendercontent.Document,
) (bool, error) {
	if config {
		metadata := snapshotFileMetadata(entry)
		if metadata.Path != source.Path || metadata.Kind != source.Kind ||
			metadata.ReloadOnChange != source.ReloadOnChange || metadata.Size != source.Size {
			return false, nil
		}
		return snapshotFileMatchesDocument(entry, document)
	}
	file, err := materializeSnapshotFileEntry(entry)
	return err == nil && exactFile(file, *source), err
}

func documentFileEntry(
	authority *Authority,
	file *File,
	config bool,
	document rendercontent.Document,
) (*snapshotEntry[File], error) {
	if config {
		return sealSnapshotDocumentFileEntry(
			authority, snapshotKey{}, file, document, false,
		)
	}
	return sealSnapshotEntry(
		authority, fileSnapshotCollection, snapshotKey{}, ownFile(*file),
	), nil
}

func documentPlanChangesStructural(
	sections []*sealedSequenceChange[Section],
	backends []*sealedMapChange[Backend],
	profiles []*sealedMapChange[Profile],
	mapsChanges []*sealedMapChange[Map],
	crtLists []*sealedMapChange[CRTList],
	files []*sealedSequenceChange[File],
) bool {
	return sequenceChangesStructural(sections) || sectionIdentityChangesStructural(sections) ||
		mapChangesStructural(backends) || mapChangesStructural(profiles) ||
		mapChangesStructural(mapsChanges) || mapChangesStructural(crtLists) ||
		sequenceChangesStructural(files) || fileChangesStructural(files)
}

func sectionIdentityChangesStructural(changes []*sealedSequenceChange[Section]) bool {
	for _, change := range changes {
		if change != nil && change.before != nil && change.after != nil {
			before := change.before.value.value
			after := change.after.value.value
			if before.Kind != after.Kind || before.Name != after.Name {
				return true
			}
		}
	}
	return false
}
