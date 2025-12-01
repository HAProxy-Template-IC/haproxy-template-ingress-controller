# Considerations

## Assumptions

This software acts as ingress controller for a fleet of HAProxy load-balancers.
It continuously watches a list of user-defined Kubernetes resource types and uses that as input to render the HAProxy
main configuration file `haproxy.cfg` via templating engine and an optional amount of auxiliary files like custom error pages (`500.http`)
or map files for lookups (`host.map`).
After rendering the files they are validated and pushed via HAProxy Dataplane API (https://www.haproxy.com/documentation/haproxy-data-plane-api/).
By pushing only changed config parts via specialized API endpoints we can prevent unnecessary HAProxy reloads.
Many specialized endpoints use the HAProxy runtime socket (https://www.haproxy.com/documentation/haproxy-runtime-api/)
under the hood and perform changes at runtime.
The template rendering is triggered by changed Kubernetes resources.
Additionally, a drift prevention monitor periodically triggers deployments (default 60s interval) to detect and correct configuration drift caused by external changes.
If any rendered file differs from the rendered files of the last run dataplane a sync is triggered.
The drift prevention mechanism ensures the controller's desired configuration is eventually consistent with the actual HAProxy configuration.

## Constraints

The dataplane API does not support all config statements that the HAProxy config language supports
(see https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/).
Therefore, only rendered configurations that can be parsed by the dataplane API library
(https://github.com/haproxytech/client-native) are supported.

## System Environment

The software is designed to be run inside a Kubernetes container.
The target dataplane APIs must also run as Kubernetes container with an HAProxy sidecar.
The Kubernetes service account of the ingress controller pod must be able to read all watched configurable resources cluster-wide.
