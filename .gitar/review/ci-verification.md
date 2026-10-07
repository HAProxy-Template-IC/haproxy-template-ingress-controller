# CI verification review

For CI changes, check that all required jobs still run before a merge, that missing
or skipped required results block verification, and that publication consumes
evidence for the exact merged tree. Reject changes that let a label, variable,
local test result, or budget shortage bypass required validation.
