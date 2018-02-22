#!/bin/bash

set -ex
set -o errexit
set -o nounset
set -o pipefail


hubs="quay.io/calico"
local_tag=$(date +%Y%m%d%H%M%S)
git_commit=$(git rev-parse --short HEAD)
image="pilot-webhook"
tags="${local_tag},${git_commit},latest"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -tag) tags="$2"; shift ;;
        -hub) hubs="$2"; shift ;;
        *) ;;
    esac
    shift
done

# Collect artifacts for pushing
CGO_ENABLED=0 GOOS=linux go build

# Build and push images

IFS=',' read -ra tags <<< "${tags}"
IFS=',' read -ra hubs <<< "${hubs}"

local_image="${image}:${local_tag}"
docker build -q -f "Dockerfile.release" -t "${local_image}" .
for tag in ${tags[@]}; do
    for hub in ${hubs[@]}; do
        tagged_image="${hub}/${image}:${tag}"
        docker tag "${local_image}" "${tagged_image}"
        docker push "${tagged_image}"
    done
done

echo "Pushed images to $hub with tags ${tags[@]}"