#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Build the docker image
docker build -t exploder .

# start kubernetes service account
oc create -f kube/sa-exploder.yaml

# start k8s pod with the api token for the above service account
oc create -f kube/dc-exploder.yaml

oadm policy add-scc-to-user privileged system:serviceaccount:default:exploder
