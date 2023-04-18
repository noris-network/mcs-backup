## Deploy Demo Cluster

Prerequisites:
  * docker
  * [kubectl][kubectl]
  * [k3d][k3d]

The demo can be started with
```
./test/run init demo
```
from the repository root. This will deploy a single-node [k3s][k3s] cluster
using [k3d][k3d] and build the generic mcs-backup docker image. A typical
environment for running mcs-backup will automatically be installed on this
cluster. This environment consists of prometheus, influxdb and grafana. After
the deployment, two fake environments ([dev and prod][envs]) are running which
make backups of a demo "application" (nginx and a script which randomly modifies
files) every 5 minutes. To access Grafana, a local port must be redirected with
`kubectl` to the cluster. Grafana can then be accessed at http://localhost:3000
```
# access grafana on localhost:3000
kubectl port-forward -n grafana svc/grafana 3000
```

[k3d]:     https://k3d.io/
[k3s]:     https://k3s.io/
[kubectl]: https://kubernetes.io/docs/tasks/tools/
[envs]:    deploy/demo/kustomization.yaml
