Start the development environment via "start-dev-env.sh" (if not already started) and check the following things:

1. Did the cluster start up?
2. Did all required pods start?
3. Do all pods logs look nominal or are there any warnings, errors or other unexpected log entries? Grep the log for "error" and "warn".
4. Does the internal state fetched via management CLI of the ingress controller look as expected?
