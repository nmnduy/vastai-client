# Secrets to setup in kubernetes

Add these to `./secrets' folder, with acquired value

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

```yaml
# kubernetes/secrets/aws-ecr-user.yml
apiVersion: v1
kind: Secret
metadata:
  name: aws-ecr-user
type: Opaque
data:
  AWS_ACCESS_KEY_ID: xxxxxxxxxxxxxxxxxxxxxxxxxxx
  AWS_SECRET_ACCESS_KEY: xxxxxxxxxxxxxxxxxxxxxxxxxxx
```
