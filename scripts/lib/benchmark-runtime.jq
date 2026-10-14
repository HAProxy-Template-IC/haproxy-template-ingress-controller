def measured_containers:
  (.initContainers[]? | select(.restartPolicy == "Always")), .containers[];

def benchmark_runtime_matches($defaults):
  ((.resources.limits.memory? // "") == "") and
  ((.resources.limits.cpu? // "") == "") and
  ([.env[]? | select(.name == "GOMEMLIMIT" or .name == "GOMAXPROCS" or
                    .name == "GOGC" or .name == "TMPDIR")] ==
   if .name == "validators" or .name == "spoa-hub" then
     [{name: "GOGC", value: ($defaults.spoaHub.hub.goGCPercent | tostring)}]
   else [] end);
