#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Build the docker image
<<<<<<< HEAD
docker build -t exploder .

# start kubernetes service account
oc create -f os-exploder-serviceaccount.yaml

# start k8s pod with the api token for the above service account
oc create -f os-exploder-deploymentconfig.yaml

oadm policy add-scc-to-user privileged system:serviceaccount:default:exploder
