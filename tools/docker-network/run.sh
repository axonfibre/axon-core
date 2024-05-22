#!/bin/bash

# Create a function to join an array of strings by a given character
function join {
  local IFS="$1"
  shift
  echo "$*"
}

# Initialize variables
MONITORING=0
MINIMAL=0

# Loop over all arguments
for arg in "$@"; do
  case $arg in
  monitoring=*)
    MONITORING="${arg#*=}"
    shift
    ;;
  minimal=*)
    MINIMAL="${arg#*=}"
    shift
    ;;
  *)
    # Unknown option
    echo "Unknown argument: $arg"
    echo 'Call with ./run.sh [monitoring=0|1] [minimal=0|1]'
    exit 1
    ;;
  esac
done

export DOCKER_BUILDKIT=1
export COMPOSE_DOCKER_CLI_BUILD=1
echo "Build iota-core"

# Setup necessary environment variables.
export DOCKER_BUILD_CONTEXT="../../"
export DOCKERFILE_PATH="./Dockerfile.dev"

if [[ "$WITH_GO_WORK" -eq 1 ]]; then
  export DOCKER_BUILD_CONTEXT="../../../"
  export DOCKERFILE_PATH="./iota-core/Dockerfile.dev"
fi

mkdir -p docker-network-snapshots/
# Allow 'others' to write, so a snapshot can be created via the management API from within docker containers.
chmod o+w docker-network-snapshots/

# Allow docker compose to build and cache an image
echo "Building docker image"
echo $DOCKER_BUILD_CONTEXT $DOCKERFILE_PATH
docker compose build --build-arg WITH_GO_WORK=${WITH_GO_WORK:-0} --build-arg DOCKER_BUILD_CONTEXT=${DOCKER_BUILD_CONTEXT} --build-arg DOCKERFILE_PATH=${DOCKERFILE_PATH}

# check exit code of builder
if [ $? -ne 0 ]; then
  echo "Building failed. Please fix and try again!"
  exit 1
fi

docker compose pull inx-indexer inx-mqtt inx-blockissuer inx-faucet inx-validator-1 inx-dashboard-1

# Remove old snapshot file
rm docker-network-snapshots/*.snapshot

# create snapshot file
echo "Create snapshot"

# Run Go command in Docker container
docker run --rm \
  --user $(id -u) \
  -v "$(realpath $(pwd)/../../):/workspace" \
  -v "${HOME}/.cache/go-build:/go-cache" \
  -v "${HOME}/go/pkg/mod:/go-mod-cache" \
  -e GOCACHE="/go-cache" \
  -e GOMODCACHE="/go-mod-cache" \
  -w "/workspace/tools/genesis-snapshot" \
  golang:1.22-bookworm go run -tags=rocksdb . --config docker --seed 7R1itJx5hVuo9w9hjg5cwKFmek4HMSoBDgJZN8hKGxih

# Move and set permissions for the .snapshot file
if ! mv -f ../genesis-snapshot/docker-network.snapshot docker-network-snapshots/snapshot.bin; then
  echo "Failed to create or move snapshot file"
  exit 1
fi

chmod o+r docker-network-snapshots/snapshot.bin

echo "Run iota-core network"

# Profiles is created to set which docker profiles to run
# https://docs.docker.com/compose/profiles/
PROFILES=()
if [ $MONITORING -ne 0 ]; then
  PROFILES+=("monitoring")
  echo "Monitoring profile active"
fi

if [ $MINIMAL -ne 0 ]; then
  echo "Minimal profile active"
else
  PROFILES+=("full")
  echo "Full profile active"
fi

export COMPOSE_PROFILES=$(join , ${PROFILES[@]})
echo $COMPOSE_PROFILES

docker compose up

echo "Clean up docker resources"
docker compose down -v
