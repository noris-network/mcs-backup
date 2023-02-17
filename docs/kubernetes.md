## File backup

```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: application-container
          [...]
        - name: mcs-backup
          envFrom:
            - configMapRef:
                name: pv-backup-config-env
            - secretRef:
                name: pv-backup-auth-env
          image: nxcc/mcs-backup:v1.5.1
          livenessProbe:
            httpGet:
              path: /healthz
              port: backup-metrics
            initialDelaySeconds: 5
            periodSeconds: 5
          ports:
            - containerPort: 9000
              name: backup-metrics
          volumeMounts:
            - mountPath: /mnt
              name: data-files
```