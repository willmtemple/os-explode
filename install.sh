#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Build the docker image
docker build -t os-exploder .

# start kubernetes service account
kubectl create -f os-exploder-sa.yaml

# start k8s pod with the api token for the above service account
kubectl create -f os-exploder-pod.yaml
