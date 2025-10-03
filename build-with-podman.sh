#!/bin/bash

# Script to build container images using podman and output Skaffold-compatible build JSON
# Usage: ./build-with-podman.sh [OPTIONS]

set -euo pipefail

# Default values
DOCKERFILE="Dockerfile"
IMAGE_NAME=""
TAG=""
OUTPUT_FILE=""
PUSH_IMAGE=false
BUILD_CONTEXT="."
PLATFORM="linux/amd64"

# Function to display usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

OPTIONS:
    -d, --dockerfile PATH    Path to Dockerfile (default: Dockerfile)
    -i, --image NAME         Image name (required)
    -t, --tag TAG           Image tag (default: latest)
    -o, --output FILE       Output JSON file path (required)
    -p, --push              Push image after building
    -c, --context PATH      Build context directory (default: .)
    --platform PLATFORM    Target platform (default: linux/amd64)
    -h, --help              Show this help message

Examples:
    $0 -i harbor.cyverse.org/de/formation -t v1.0.0 -o build.json
    $0 -i harbor.cyverse.org/de/formation -t latest -o artifacts.json -p
    $0 -d custom.Dockerfile -i harbor.cyverse.org/de/formation -o build.json -c /path/to/context
EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--dockerfile)
            DOCKERFILE="$2"
            shift 2
            ;;
        -i|--image)
            IMAGE_NAME="$2"
            shift 2
            ;;
        -t|--tag)
            TAG="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        -p|--push)
            PUSH_IMAGE=true
            shift
            ;;
        -c|--context)
            BUILD_CONTEXT="$2"
            shift 2
            ;;
        --platform)
            PLATFORM="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown option $1" >&2
            usage
            exit 1
            ;;
    esac
done

# Validate required arguments
if [[ -z "$IMAGE_NAME" ]]; then
    echo "Error: Image name is required (-i/--image)" >&2
    usage
    exit 1
fi

if [[ -z "$OUTPUT_FILE" ]]; then
    echo "Error: Output file is required (-o/--output)" >&2
    usage
    exit 1
fi

# Set default tag if not provided
if [[ -z "$TAG" ]]; then
    TAG="latest"
fi

# Construct full image tag
FULL_IMAGE_TAG="${IMAGE_NAME}:${TAG}"

# Validate Dockerfile exists
if [[ ! -f "$BUILD_CONTEXT/$DOCKERFILE" ]]; then
    echo "Error: Dockerfile not found at $BUILD_CONTEXT/$DOCKERFILE" >&2
    exit 1
fi

# Validate build context exists
if [[ ! -d "$BUILD_CONTEXT" ]]; then
    echo "Error: Build context directory not found: $BUILD_CONTEXT" >&2
    exit 1
fi

echo "Building image: $FULL_IMAGE_TAG"
echo "Dockerfile: $BUILD_CONTEXT/$DOCKERFILE"
echo "Build context: $BUILD_CONTEXT"
echo "Platform: $PLATFORM"

# Build the image using podman
podman build \
    -t "$FULL_IMAGE_TAG" \
    -f "$BUILD_CONTEXT/$DOCKERFILE" \
    --platform "$PLATFORM" \
    "$BUILD_CONTEXT"

# Push the image if requested
if [[ "$PUSH_IMAGE" == "true" ]]; then
    echo "Pushing image: $FULL_IMAGE_TAG"
    podman push "$FULL_IMAGE_TAG"
fi

# Create the build JSON output in Skaffold format
echo "Writing build JSON to: $OUTPUT_FILE"
cat > "$OUTPUT_FILE" << EOF
{
  "builds": [
    {
      "imageName": "$IMAGE_NAME",
      "tag": "$FULL_IMAGE_TAG"
    }
  ]
}
EOF

echo "Build completed successfully!"
echo "Image: $FULL_IMAGE_TAG"
echo "Build JSON written to: $OUTPUT_FILE"
