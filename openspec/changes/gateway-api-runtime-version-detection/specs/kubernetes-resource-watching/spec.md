# Kubernetes Resource Watching — Delta

## MODIFIED Requirements

### Requirement: Watch Error Handling

The SingleWatcher and the Bulk Watcher SHALL register a watch error handler for observability. Watch errors SHALL be logged and the last error timestamp SHALL be recorded per watcher. The informer's built-in Reflector SHALL handle retry with exponential backoff automatically. Callback failures SHALL be logged but SHALL NOT stop the watcher.

#### Scenario: Watch error logged and timestamp recorded

- **WHEN** a watch connection error occurs on a SingleWatcher
- **THEN** the error SHALL be logged at warn level and LastWatchError SHALL return a non-zero timestamp.

#### Scenario: Callback failure does not stop watcher

- **WHEN** an OnChange callback returns an error
- **THEN** the SingleWatcher SHALL log the error and continue watching for further events.

#### Scenario: Bulk watcher surfaces watch errors

- **WHEN** a watch connection error occurs on a Bulk Watcher (for example because the watched API version stopped being served mid-run)
- **THEN** the error SHALL be logged at warn level with the watcher's GVR and the last error timestamp SHALL be recorded, instead of the failure being visible only in client-go's internal logging.
