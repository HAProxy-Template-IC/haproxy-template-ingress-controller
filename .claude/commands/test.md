Run all tests and linters and fix all outstanding issues. Do not ignore issues that have not been caused by the current code changes. All tests must pass.

Run the unit tests first via `make test` and after all failures have been fixed run the full test suite including integration tests via `make test-integration`.

When adding or modifying tests, follow table-driven test patterns and reuse test helpers where possible.

After everything is fixed, re-run all tests and linters one last time to make sure via `make check-all`.