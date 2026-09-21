# Manage agent certificates

The default chart creates and automatically renews the certificate authority (CA)
and both controller-to-agent identities. It doesn't require cert-manager. You can
select cert-manager explicitly or supply externally managed identity Secrets.
Both peers reload mounted certificates without restarting.

The commands below use Bash, `kubectl`, `jq`, Python 3, and OpenSSL, with release
`haptic` in namespace `haptic`. You need permission to inspect Secrets and Jobs;
manual certificate replacement also requires updating Secrets and exec access.
For the authentication model, see [Security](./security.md#credentials).

## Default renewal

The bootstrap Job creates certificates after chart preflight validation. An
hourly CronJob checks them and renews the CA, its key, and both identity keys when
30 days remain. With `haproxy.agent.tls.certValidityDays` below 90 days, renewal
starts with one third of the lifetime remaining. The default lifetime is 365 days.
Changing the value affects the next issuance; it doesn't immediately replace
certificates that aren't due for renewal.

The issuer Secret stores the private CA key and the current certificate
generation. Back up this Secret securely. Neither controller nor agent mounts
it. Helm upgrades and offline rendering preserve certificates because jobs
reconcile the durable state through the Kubernetes API.

During renewal, the new identities can authenticate against the old CA for one
hour, and both peers temporarily trust the previous CA. This covers staggered
Secret projections. Previous trust expires even if a later cleanup Job fails.
An interrupted Job resumes the stored generation instead of creating another CA.

!!! warning "Monitor renewal failures"
    Authentication stops if an identity expires or a partially completed CA
    rotation outlasts the one-hour trust overlap.
    The controller can't deliver routing, endpoint, or certificate updates, while
    running HAProxy workers retain their last configuration. Fix the failed Job
    and rerun it; renewal uses Kubernetes credentials and can recover expired
    agent identities. Local socket liveness probes keep the agent running during
    certificate failure so it can load the repaired identity.

Inspect recent runs:

```bash
kubectl get cronjob haptic-agent-renewal -n haptic
kubectl get jobs -n haptic --sort-by=.metadata.creationTimestamp
```

Run a check immediately:

```bash
job="haptic-agent-renewal-$(date +%s)"
kubectl create job "$job" --from=cronjob/haptic-agent-renewal -n haptic
kubectl wait --for=condition=complete "job/$job" -n haptic --timeout=5m
kubectl logs "job/$job" -n haptic
```

Don't patch internally managed identity data: the next Job restores its saved
generation. A missing identity Secret is recreated. A missing or corrupt issuer
Secret requires restoring its backup; the Job refuses to replace existing
identities with an unrelated CA. Runtime-created Secrets survive Helm uninstall.

## Use cert-manager

Install cert-manager before selecting this mode. The chart doesn't auto-detect
it or change an existing installation's issuer when cert-manager appears.

For a new release:

```bash
helm install haptic \
  oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic --create-namespace \
  --set haproxy.agent.tls.certManager.enabled=true
```

The chart creates a self-signed root `Certificate`, a CA `Issuer`, and separate
server and client `Certificate` resources. Cert-manager renews the identities
30 days before expiry and replaces their private keys. For lifetimes below
90 days, renewal starts with one third of the lifetime remaining.

The root certificate lasts at least ten years and renews with the **same CA
key**, at least two identity lifetimes before expiry. Subsequent identity
renewals distribute the renewed root certificate. This renews the root
certificate; it doesn't rotate the CA key. Cert-manager's CA issuer alone
[doesn't renew its CA or immediately replace dependent certificates](https://cert-manager.io/docs/configuration/ca/#important-information).

To select your existing issuer, use these values:

```yaml
haproxy:
  agent:
    tls:
      certManager:
        enabled: true
        createIssuer: false
        issuerRef:
          name: organisation
          kind: ClusterIssuer
          group: cert-manager.io
```

The issuer must issue both TLS roles and populate `ca.crt` in each identity
Secret. You own that issuer's CA lifetime and trust transitions.

To change providers on an existing release, use new issuer and identity Secret
names and expect a controller and agent rollout. Keep the old Secrets until
both workloads are ready. A provider must not take over another provider's
Secrets.

To switch from the default manager to the chart-created cert-manager issuer,
install cert-manager, then run:

```bash
umask 077
helm get values haptic --namespace haptic --output yaml > haptic-current-values.yaml
helm upgrade haptic \
  oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic --reset-values --values haptic-current-values.yaml \
  --set haproxy.agent.tls.certManager.enabled=true \
  --set haproxy.agent.tls.issuerSecretName=haptic-cm-issuer \
  --set haproxy.agent.tls.serverSecretName=haptic-cm-agent \
  --set haproxy.agent.tls.clientSecretName=haptic-cm-controller
kubectl rollout status deployment/haptic-controller -n haptic
kubectl rollout status deployment/haptic-haproxy -n haptic
```

## Check expiry

Check both identities and their CA. This command prints expiry
dates and exits with an error if any certificate expires within 30 days or can't
be read. It reads only public certificates from the Secrets.

```bash
set -euo pipefail
needs_rotation=0
for deployment in haptic-controller haptic-haproxy; do
  secret=$(kubectl get deployment "$deployment" -n haptic -o json |
    jq -er '.spec.template.spec.volumes[] | select(.name == "agent-tls").secret.secretName')
  for key in tls.crt ca.crt; do
    printf '%s %s: ' "$secret" "$key"
    if ! kubectl get secret "$secret" -n haptic -o json |
      jq -er --arg key "$key" '.data[$key]' | base64 -d |
      openssl x509 -noout -enddate -checkend 2592000; then
      needs_rotation=1
    fi
  done
done
test "$needs_rotation" -eq 0
```

Run the check regularly through your monitoring system. Certificates that stay
inside the renewal window indicate a failed renewal process. With an external
issuer, monitor its CA lifetime as well as the identity lifetimes.

## Supply external identities

Prepare `agent.crt`, `agent.key`, `controller.crt`, `controller.key`, and `ca.crt`
from your certificate issuer. The agent certificate needs the `serverAuth`
extended key usage and DNS SAN `agent.example.internal`. The controller
certificate needs `clientAuth` and DNS SAN `controller.example.internal`.
`ca.crt` must trust the peer's issuer. Each certificate file includes any
intermediate certificates after its leaf certificate.

1. Create the namespace if it doesn't exist.

    ```bash
    kubectl create namespace haptic --dry-run=client -o yaml | kubectl apply -f -
    ```

2. Create the two identity Secrets.

    ```bash
    kubectl create secret generic haptic-agent-identity -n haptic \
      --type=kubernetes.io/tls \
      --from-file=tls.crt=agent.crt --from-file=tls.key=agent.key \
      --from-file=ca.crt=ca.crt
    kubectl create secret generic haptic-controller-identity -n haptic \
      --type=kubernetes.io/tls \
      --from-file=tls.crt=controller.crt --from-file=tls.key=controller.key \
      --from-file=ca.crt=ca.crt
    ```

    If your certificate manager already supplies these Secrets with these keys,
    proceed to the next step. Use your deployment's Secret names and certificate
    SANs in the values file when they differ from this example.

3. Write the TLS values.

    ```bash
    cat > agent-tls-values.yaml <<'YAML'
    haproxy:
      agent:
        tls:
          enabled: true
          managed: false
          serverSecretName: haptic-agent-identity
          clientSecretName: haptic-controller-identity
          serverName: agent.example.internal
          clientName: controller.example.internal
    YAML
    ```

4. Include the file when installing or upgrading the release.

    For a new release:

    ```bash
    helm install haptic \
      oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --namespace haptic --values agent-tls-values.yaml
    ```

    For an existing release, save its custom values and include them in the upgrade:

    ```bash
    umask 077
    helm get values haptic --namespace haptic --output yaml > haptic-current-values.yaml
    helm upgrade haptic \
      oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --namespace haptic --reset-values \
      --values haptic-current-values.yaml --values agent-tls-values.yaml
    ```

    The upgrade applies new chart defaults, your saved custom values, and then
    the TLS settings. Keep these externally supplied identities under your Secret
    manager for subsequent renewal.

## Rotate a certificate authority

This procedure applies to external Secrets you control directly with
`haproxy.agent.tls.managed: false`. Complete each stage before starting the next.
If another certificate manager owns the Secrets, coordinate these stages through
that manager so it doesn't restore the old data.

Existing identities must remain valid throughout the overlap. The example grants
one hour of overlap; HAPTIC rejects deadlines more than 24 hours away. Renewing
only leaf certificates under an unchanged CA doesn't require a trust change.
Update each `tls.crt`/`tls.key` pair atomically and wait for its projected files.

### Prepare replacement identities

1. Identify the installed Secrets and required peer names.

    ```bash
    set -euo pipefail
    umask 077
    rotation_dir=$(mktemp -d)
    server_secret=$(kubectl get deployment haptic-haproxy -n haptic -o json |
      jq -r '.spec.template.spec.volumes[] | select(.name == "agent-tls").secret.secretName')
    client_secret=$(kubectl get deployment haptic-controller -n haptic -o json |
      jq -r '.spec.template.spec.volumes[] | select(.name == "agent-tls").secret.secretName')
    agent_name=$(kubectl get deployment haptic-controller -n haptic -o json |
      jq -r '.spec.template.spec.containers[] | select(.name == "controller").env[] | select(.name == "AGENT_TLS_SERVER_NAME").value')
    controller_name=$(kubectl get deployment haptic-haproxy -n haptic -o json |
      jq -r '.spec.template.spec.initContainers[] | select(.name == "agent").args[] | select(startswith("--tls-client-name=")) | ltrimstr("--tls-client-name=")')
    test -n "$server_secret" && test -n "$client_secret"
    test -n "$agent_name" && test -n "$controller_name"
    ```

2. Generate a new CA and role-specific identities.

    ```bash
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
      -keyout "$rotation_dir/ca.key" -out "$rotation_dir/ca.crt" -days 365 \
      -subj /CN=haptic-agent-ca \
      -addext 'basicConstraints=critical,CA:TRUE' \
      -addext 'keyUsage=critical,keyCertSign,cRLSign'
    for role in agent controller; do
      name=$agent_name
      usage=serverAuth
      if [ "$role" = controller ]; then name=$controller_name; usage=clientAuth; fi
      openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
        -keyout "$rotation_dir/$role.key" -out "$rotation_dir/$role.csr" \
        -subj "/CN=$name"
      printf 'subjectAltName=DNS:%s\nextendedKeyUsage=%s\nkeyUsage=critical,digitalSignature\nbasicConstraints=critical,CA:FALSE\n' \
        "$name" "$usage" > "$rotation_dir/$role.ext"
      openssl x509 -req -in "$rotation_dir/$role.csr" \
        -CA "$rotation_dir/ca.crt" -CAkey "$rotation_dir/ca.key" \
        -set_serial "0x$(openssl rand -hex 16)" -days 365 \
        -extfile "$rotation_dir/$role.ext" -out "$rotation_dir/$role.crt"
    done
    ```

3. Define a check for the projected files on every replica.

    ```bash
    wait_projected() {
      local component=$1 container=$2 file=$3 expected=$4
      local deadline=$((SECONDS + 180)) pods pod ready
      while [ "$SECONDS" -lt "$deadline" ]; do
        pods=$(kubectl get pods -n haptic \
          -l "app.kubernetes.io/instance=haptic,app.kubernetes.io/component=$component" \
          -o json | jq -r '.items[] | select(.metadata.deletionTimestamp == null).metadata.name')
        ready=true
        [ -n "$pods" ] || ready=false
        for pod in $pods; do
          if [ "$expected" = absent ]; then
            kubectl exec -n haptic "$pod" -c "$container" -- \
              test ! -e "/etc/haptic/agent-tls/..data/$file" || ready=false
          elif ! kubectl exec -n haptic "$pod" -c "$container" -- \
            cat "/etc/haptic/agent-tls/..data/$file" | cmp -s "$expected" -; then
            ready=false
          fi
        done
        [ "$ready" = true ] && return 0
        sleep 2
      done
      echo "TLS files haven't reached every $component replica; repair projection before continuing" >&2
      return 1
    }
    ```

    [Secret projection is asynchronous](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod).
    A successful Secret update alone doesn't
    establish that every peer trusts the new CA.

### Distribute overlapping trust

1. Add the new CA while retaining each peer's previous trust with a deadline.

    ```bash
    deadline=$(python3 -c 'from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))')
    for secret in "$server_secret" "$client_secret"; do
      kubectl get secret "$secret" -n haptic -o json |
        jq -e '.data["previous-ca.crt"] == null' >/dev/null
      kubectl get secret "$secret" -n haptic -o jsonpath='{.data.ca\.crt}' |
        base64 -d > "$rotation_dir/$secret-old-ca.crt"
      jq -n --rawfile ca "$rotation_dir/ca.crt" \
        --rawfile previous "$rotation_dir/$secret-old-ca.crt" --arg until "$deadline" \
        '{data:{"ca.crt":($ca|@base64),"previous-ca.crt":($previous|@base64),"previous-ca-until":($until|@base64)}}' |
        kubectl patch secret "$secret" -n haptic --type=merge --patch-file=/dev/stdin
    done
    ```

    Complete any existing rotation before starting another one. For an
    interrupted rotation, follow the recovery steps below.

2. Wait for both sides to receive the trust bundle.

    ```bash
    wait_projected loadbalancer agent ca.crt "$rotation_dir/ca.crt"
    wait_projected controller controller ca.crt "$rotation_dir/ca.crt"
    ```

### Replace identities

1. Update each certificate and key together.

    ```bash
    for role in agent controller; do
      secret=$server_secret
      [ "$role" != controller ] || secret=$client_secret
      jq -n --rawfile cert "$rotation_dir/$role.crt" --rawfile key "$rotation_dir/$role.key" \
        '{data:{"tls.crt":($cert|@base64),"tls.key":($key|@base64)}}' |
        kubectl patch secret "$secret" -n haptic --type=merge --patch-file=/dev/stdin
    done
    ```

2. Wait for every replica to receive its new identity.

    ```bash
    wait_projected loadbalancer agent tls.crt "$rotation_dir/agent.crt"
    wait_projected controller controller tls.crt "$rotation_dir/controller.crt"
    ```

3. Verify the authenticated controller-to-agent connection.

    ```bash
    HAPROXY_IP=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].status.podIP}')
    kubectl exec -n haptic deployment/haptic-controller -c controller -- \
      haptic agent state --url "https://$HAPROXY_IP:5555"
    ```

### Revoke the previous CA

1. Remove the previous CA and deadline from both Secrets.

    ```bash
    for secret in "$server_secret" "$client_secret"; do
      kubectl patch secret "$secret" -n haptic --type=merge \
        -p '{"data":{"previous-ca.crt":null,"previous-ca-until":null}}'
    done
    wait_projected loadbalancer agent previous-ca.crt absent
    wait_projected controller controller previous-ca.crt absent
    ```

    Old clients now fail authentication, including on reused connections.
    Requests authenticated before revocation can finish within their operation
    deadlines. A retained `previous-ca.crt` also stops granting trust when its
    deadline passes.

2. Remove the temporary private keys after storing any material your certificate issuer needs.

    ```bash
    rm -r -- "$rotation_dir"
    ```

## Recover an interrupted rotation

Before the overlap expires, resume at the first incomplete stage. Check every
replica's projected files before revoking trust. Invalid certificate or trust
files reject new management operations; they don't cause an HTTP fallback.

If the deadline expires before identities are replaced, finish projecting the
new identities through the Kubernetes API. Existing HAProxy workers keep their
last valid configuration. Restore any missing external identity Secret from
your backup, or have its issuer recreate it under the intended CA.
