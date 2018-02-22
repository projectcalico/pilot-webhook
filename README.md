# Istio Pilot Webhook

A webhook for Istio Pilot that inserts the external authorization filter & Dikastes cluster into
xDS delivered to Envoy proxies.  This allows Istio to use Calico Unified Policy for authorization
in Istio.

To use this webhook, you will need to add it to your Pilot deployment and configure Pilot to call it.

1. Modify the Pilot `discovery` container spec to add `"--webhookEndpoint", "unix:///var/run/calico/webhook.sock"` to the
 `args` list.
1. Add an `emptyDir` volume called `webhook` and mount it at `/var/run/calico` in the `discovery` container.
1. Add the `pilot-webhook` container to the pod spec, including the `webhook` volume mount.

The following YAML illustrates a Pilot deployment with these changes made.

```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: istio-pilot
  namespace: istio-system
  annotations:
    sidecar.istio.io/inject: "false"
spec:
  replicas: 1
  template:
    metadata:
      labels:
        istio: pilot
      annotations:
        sidecar.istio.io/inject: "false"
    spec:
      serviceAccountName: istio-pilot-service-account
      containers:
      - name: discovery
        image: gcr.io/istio-testing/pilot:latest
        imagePullPolicy: IfNotPresent
        args: ["discovery", "-v", "2", "--admission-service", "istio-pilot", "--webhookEndpoint", "unix:///var/run/calico/webhook.sock"]
        ports:
        - containerPort: 8080
        - containerPort: 443
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.namespace
        volumeMounts:
        - name: config-volume
          mountPath: /etc/istio/config
        - name: webhook
          mountPath: /var/run/calico
      - name: istio-proxy
        image: gcr.io/istio-testing/proxy_debug:latest
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 15003
        args:
        - proxy
        - pilot
        - -v
        - "2"
        - --discoveryAddress
        - istio-pilot:15003
        - --controlPlaneAuthPolicy
        - NONE #--controlPlaneAuthPolicy
        - --customConfigFile
        - /etc/istio/proxy/envoy_pilot.json
        volumeMounts:
        - name: istio-certs
          mountPath: /etc/certs
          readOnly: true
      - name: pilot-webhook
        image: quay.io/calico/pilot-webhook:latest
        imagePullPolicy: Always
        args:
        - /pilot-webhook
        - /var/run/calico/webhook.sock
        - --debug
        volumeMounts:
        - name: webhook
          mountPath: /var/run/calico
      volumes:
      - name: config-volume
        configMap:
          name: istio
      - name: istio-certs
        secret:
          secretName: istio.istio-pilot-service-account
          optional: true
      - name: webhook
        emptyDir: {}

```