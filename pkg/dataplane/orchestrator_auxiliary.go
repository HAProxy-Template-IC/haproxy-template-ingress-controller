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

package dataplane

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// deleteObsoleteFilesPostConfig deletes obsolete auxiliary files AFTER successful config sync.
// Errors are logged as warnings but do not fail the sync since config is already applied.
func (o *orchestrator) deleteObsoleteFilesPostConfig(ctx context.Context, fileDiff *auxiliaryfiles.FileDiff, sslDiff *auxiliaryfiles.SSLCertificateDiff, caFileDiff *auxiliaryfiles.SSLCaFileDiff, mapDiff *auxiliaryfiles.MapFileDiff) {
	// Delete general files
	if fileDiff != nil && len(fileDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete general files", "count", len(fileDiff.ToDelete))

		postConfigDiff := &auxiliaryfiles.FileDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: fileDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncGeneralFiles(ctx, o.client, postConfigDiff); err != nil {
			o.logger.Warn("Failed to delete obsolete general files", "error", err, "files", fileDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete general files deleted successfully")
		}
	}

	// Delete SSL certificates
	if sslDiff != nil && len(sslDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete SSL certificates", "count", len(sslDiff.ToDelete))

		postConfigSSL := &auxiliaryfiles.SSLCertificateDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: sslDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncSSLCertificates(ctx, o.client, postConfigSSL); err != nil {
			o.logger.Warn("Failed to delete obsolete SSL certificates", "error", err, "certificates", sslDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete SSL certificates deleted successfully")
		}
	}

	// Delete SSL CA files
	if caFileDiff != nil && len(caFileDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete SSL CA files", "count", len(caFileDiff.ToDelete))

		postConfigCA := &auxiliaryfiles.SSLCaFileDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: caFileDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncSSLCaFiles(ctx, o.client, postConfigCA); err != nil {
			o.logger.Warn("Failed to delete obsolete SSL CA files", "error", err, "ca_files", caFileDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete SSL CA files deleted successfully")
		}
	}

	// Delete map files
	if mapDiff != nil && len(mapDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete map files", "count", len(mapDiff.ToDelete))

		postConfigMap := &auxiliaryfiles.MapFileDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: mapDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncMapFiles(ctx, o.client, postConfigMap); err != nil {
			o.logger.Warn("Failed to delete obsolete map files", "error", err, "maps", mapDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete map files deleted successfully")
		}
	}

	// Note: CRT-list deletion is handled by the general files deletion above.
	// Since CRT-lists are stored as general files (to avoid reload on create),
	// they are merged into the general files comparison and deleted together.
	// The crtlistDiff.ToDelete is cleared in compareAuxiliaryFiles() to prevent
	// conflicting delete operations between general files and CRT-lists.
}

// auxiliaryFileSyncParams contains parameters for auxiliary file synchronization.
type auxiliaryFileSyncParams struct {
	resourceType string
	creates      int
	updates      int
	deletes      int
	stage        string
	message      string
	hints        []string
	syncFunc     func(context.Context) ([]string, error) // Returns (reloadIDs, error)
}

// syncAuxiliaryFileType is a helper that executes auxiliary file sync with the common pattern.
// It logs changes, executes the sync function, handles errors, and logs success.
// Returns reload IDs from create/update operations that triggered reloads.
func (o *orchestrator) syncAuxiliaryFileType(ctx context.Context, params *auxiliaryFileSyncParams) ([]string, error) {
	o.logger.Debug(params.resourceType+" changes detected",
		"creates", params.creates,
		"updates", params.updates,
		"deletes", params.deletes)

	reloadIDs, err := params.syncFunc(ctx)
	if err != nil {
		return nil, &SyncError{
			Stage:   params.stage,
			Message: params.message,
			Cause:   err,
			Hints:   params.hints,
		}
	}

	o.logger.Debug(params.resourceType+" synced successfully (pre-config phase)",
		"reload_ids", len(reloadIDs))
	return reloadIDs, nil
}

// scheduleAuxiliarySync schedules an auxiliary file sync task in the errgroup and collects reload IDs.
// This helper reduces cognitive complexity by extracting the common goroutine pattern.
func (o *orchestrator) scheduleAuxiliarySync(
	g *errgroup.Group,
	ctx context.Context,
	params *auxiliaryFileSyncParams,
	reloadIDs *[]string,
	mu *sync.Mutex,
) {
	g.Go(func() error {
		ids, err := o.syncAuxiliaryFileType(ctx, params)
		if err != nil {
			return err
		}
		mu.Lock()
		*reloadIDs = append(*reloadIDs, ids...)
		mu.Unlock()
		return nil
	})
}

// syncAuxiliaryFilesPreConfig syncs all auxiliary files before config sync (Phase 1).
// Only creates and updates are synced; deletions are deferred until post-config phase.
// Returns reload IDs from create/update operations that triggered reloads.
//
// IMPORTANT: SSL certificates and CA files are synced FIRST (synchronously) before other aux files.
// This ordering prevents a race condition where CRT-list, map, or general file syncs
// trigger a HAProxy reload before all SSL certificates/CA files are uploaded. When HAProxy reloads,
// it validates the current config which may reference SSL certs/CA files still being uploaded in parallel.
// By syncing SSL certs and CA files first, we ensure they exist before any reload can be triggered.
//
// NOTE: CRT-list files are NOT synced separately here. They are merged into general files
// in compareAuxiliaryFiles() for unified storage. The crtlistDiff is used by callers
// for metrics and logging only, not for actual sync.
func (o *orchestrator) syncAuxiliaryFilesPreConfig(
	ctx context.Context,
	fileDiff *auxiliaryfiles.FileDiff,
	sslDiff *auxiliaryfiles.SSLCertificateDiff,
	caFileDiff *auxiliaryfiles.SSLCaFileDiff,
	mapDiff *auxiliaryfiles.MapFileDiff,
) ([]string, error) {
	var allReloadIDs []string
	var mu sync.Mutex

	// Phase 1a: Sync SSL certificates FIRST (synchronously)
	// SSL certificates must exist before any other aux file sync triggers a reload,
	// because the HAProxy config may reference these certificates.
	if sslDiff != nil && sslDiff.HasChanges() {
		reloadIDs, err := o.syncAuxiliaryFileType(ctx, &auxiliaryFileSyncParams{
			resourceType: "SSL certificate",
			creates:      len(sslDiff.ToCreate),
			updates:      len(sslDiff.ToUpdate),
			deletes:      len(sslDiff.ToDelete),
			stage:        "sync_ssl_pre",
			message:      "failed to sync SSL certificates before config sync",
			hints: []string{
				"Check SSL storage permissions",
				"Verify certificate contents are valid PEM format",
				"Review error message for specific certificate failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigSSL := &auxiliaryfiles.SSLCertificateDiff{
					ToCreate: sslDiff.ToCreate,
					ToUpdate: sslDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncSSLCertificates(ctx, o.client, preConfigSSL)
			},
		})
		if err != nil {
			return nil, err
		}
		allReloadIDs = append(allReloadIDs, reloadIDs...)
	}

	// Phase 1a (continued): Sync SSL CA files (synchronously, before other aux files)
	// CA files must exist before any other aux file sync triggers a reload,
	// because the HAProxy config may reference these CA files for client verification.
	if caFileDiff != nil && caFileDiff.HasChanges() {
		reloadIDs, err := o.syncAuxiliaryFileType(ctx, &auxiliaryFileSyncParams{
			resourceType: "SSL CA file",
			creates:      len(caFileDiff.ToCreate),
			updates:      len(caFileDiff.ToUpdate),
			deletes:      len(caFileDiff.ToDelete),
			stage:        "sync_ssl_ca_pre",
			message:      "failed to sync SSL CA files before config sync",
			hints: []string{
				"Check SSL CA storage permissions",
				"Verify CA certificate contents are valid PEM format",
				"SSL CA file storage requires DataPlane API v3.2+",
				"Review error message for specific CA file failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigCA := &auxiliaryfiles.SSLCaFileDiff{
					ToCreate: caFileDiff.ToCreate,
					ToUpdate: caFileDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncSSLCaFiles(ctx, o.client, preConfigCA)
			},
		})
		if err != nil {
			return nil, err
		}
		allReloadIDs = append(allReloadIDs, reloadIDs...)
	}

	// Phase 1b: Sync remaining aux files in parallel
	// Now that SSL certs exist, other aux file syncs can safely trigger reloads.
	g, gCtx := errgroup.WithContext(ctx)

	// Sync general files if there are changes
	if fileDiff != nil && fileDiff.HasChanges() {
		o.scheduleAuxiliarySync(g, gCtx, &auxiliaryFileSyncParams{
			resourceType: "General file",
			creates:      len(fileDiff.ToCreate),
			updates:      len(fileDiff.ToUpdate),
			deletes:      len(fileDiff.ToDelete),
			stage:        "sync_files_pre",
			message:      "failed to sync general files before config sync",
			hints: []string{
				"Check HAProxy storage is writable",
				"Verify file contents are valid",
				"Review error message for specific file failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigDiff := &auxiliaryfiles.FileDiff{
					ToCreate: fileDiff.ToCreate,
					ToUpdate: fileDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncGeneralFiles(ctx, o.client, preConfigDiff)
			},
		}, &allReloadIDs, &mu)
	}

	// Sync map files if there are changes
	if mapDiff != nil && mapDiff.HasChanges() {
		o.scheduleAuxiliarySync(g, gCtx, &auxiliaryFileSyncParams{
			resourceType: "Map file",
			creates:      len(mapDiff.ToCreate),
			updates:      len(mapDiff.ToUpdate),
			deletes:      len(mapDiff.ToDelete),
			stage:        "sync_maps_pre",
			message:      "failed to sync map files before config sync",
			hints: []string{
				"Check map storage permissions",
				"Verify map file format is correct",
				"Review error message for specific map failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigMap := &auxiliaryfiles.MapFileDiff{
					ToCreate: mapDiff.ToCreate,
					ToUpdate: mapDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncMapFiles(ctx, o.client, preConfigMap)
			},
		}, &allReloadIDs, &mu)
	}

	// NOTE: CRT-list files are NOT synced separately here.
	// They are merged into general files in compareAuxiliaryFiles() for unified storage.
	// The crtlistDiff is used for metrics and logging only, not for actual sync.
	// See: compareAuxiliaryFiles() lines 920-927 where CRT-lists are merged into general files.

	// Wait for all remaining auxiliary file syncs to complete
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return allReloadIDs, nil
}
