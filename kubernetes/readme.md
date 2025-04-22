# Setting Up The Registry ⚙️

For internal cluster operations (pulling images for deployments), it's best practice to use the `ClusterIP` service address. This keeps traffic routing within the cluster's overlay network and doesn't depend on specific node IPs.

*   **SSH into each K3s node.**
*   **Edit or create `/etc/rancher/k3s/registries.yaml`:** Use the `ClusterIP` service's DNS name (`<service-name>.<namespace>.svc.cluster.local:<port>` or the shorter `<service-name>.<namespace>:<port>`).

    ```yaml
    mirrors:
      # Use the ClusterIP service name and port
      "docker-registry-internal-svc.registry:5000":
        endpoint:
          # Use http:// because it's insecure
          - "http://docker-registry-internal-svc.registry:5000"

    # Or using the alternative format:
    # configs:
    #  "docker-registry-internal-svc.registry:5000":
    #    tls:
    #      insecure_skip_verify: true
    ```

*   **Restart the K3s service** on each node:

    ```bash
    # On servers (masters)
    sudo systemctl restart k3s

    # On agents (workers)
    sudo systemctl restart k3s-agent
    ```

## Configure Your Local Docker Daemon (for Pushing) - Use NodePort Address

On the machine where you will run `docker push`, configure the Docker daemon to trust the *NodePort* address as insecure.

*   **Edit `/etc/docker/daemon.json`** (or equivalent for Docker Desktop): Add the `NodeIP:NodePort` you'll use for pushing (e.g., `192.168.1.100:30050`).

    ```json
    {
      "insecure-registries" : ["192.168.1.100:30050"]
    }
    ```
*   **Restart your local Docker daemon.**

## Pushing vs. Pulling (Usage)

*   **Pushing (from your external machine):** Tag and push using the `NodeIP:NodePort` address.

    ```bash
    docker tag your-app-server:latest 192.168.1.100:30050/your-app-server:latest
    docker push 192.168.1.100:30050/your-app-server:latest
    ```

*   **Pulling (in Kubernetes Deployments):** Reference images in your deployment YAMLs using the `ClusterIP` service address that you configured in `registries.yaml`.

    ```yaml
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: my-server-deployment
      # ...
    spec:
      # ...
      template:
        # ...
        spec:
          containers:
            - name: server
              # Use the ClusterIP service address K3s nodes are configured to trust
              image: docker-registry-internal-svc.registry:5000/your-app-server:latest
              # ...
    ```

## Secrets to setup in kubernetes

```yaml
# kubernetes/secrets/r2-credentials.yml
apiVersion: v1
kind: Secret
metadata:
  name: r2-credentials
type: Opaque
data:
  LITESTREAM_ACCESS_KEY_ID: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
  LITESTREAM_SECRET_ACCESS_KEY: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx=
---
apiVersion: v1
kind: Secret
metadata:
  name: r2-api-token
type: Opaque
data:
  TOKEN: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx=
```
