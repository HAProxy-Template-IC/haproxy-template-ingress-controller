## Appendices and References

#### Definitions and Abbreviations

- **API**: Application Programming Interface
- **CRD**: Custom Resource Definition - Kubernetes extension mechanism
- **Dataplane API**: HAProxy's management interface for runtime configuration
- **GVR**: GroupVersionResource - Kubernetes resource identifier
- **HAProxy**: High Availability Proxy - Load balancer and reverse proxy
- **IC**: Ingress Controller
- **Informer**: Kubernetes client-go pattern for watching and caching resources
- **O(1)**: Constant time complexity - performance independent of input size
- **Runtime API**: HAProxy's socket-based interface for zero-reload updates
- **SharedInformerFactory**: client-go factory for creating resource watchers with shared caches

#### References

1. **HAProxy Documentation**
   - Configuration Manual: https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/
   - Dataplane API: https://www.haproxy.com/documentation/haproxy-data-plane-api/
   - Runtime API: https://www.haproxy.com/documentation/haproxy-runtime-api/

2. **HAProxy Go Libraries**
   - client-native: https://github.com/haproxytech/client-native
     - Configuration parser and validator
     - Used for syntax validation without running HAProxy
   - dataplaneapi: https://github.com/haproxytech/dataplaneapi
     - Reference implementation for validation strategies
     - Configuration management patterns

3. **Kubernetes Client Libraries**
   - client-go: https://github.com/kubernetes/client-go
     - Official Kubernetes Go client
     - Informer pattern documentation
   - apimachinery: https://github.com/kubernetes/apimachinery
     - Common machinery for Kubernetes API interactions

4. **Template Engines**
   - gonja v2: https://github.com/nikolalohinski/gonja
     - Pure Go Jinja2 template engine, actively maintained
     - Latest release: v2.4.1 (January 2025)
     - Recommended for Jinja2-compatible templating

5. **Observability**
   - Prometheus client_golang: https://github.com/prometheus/client_golang
   - OpenTelemetry Go: https://github.com/open-telemetry/opentelemetry-go

6. **Design Patterns**
   - Kubernetes Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
   - Controller Pattern: https://kubernetes.io/docs/concepts/architecture/controller/

