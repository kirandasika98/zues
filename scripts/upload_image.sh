#!/bin/bash
# Script constants
SERVICE_NAME="sysz"

# Global variables
FINAL_IMAGE=""

docker_build()
{
    GIT_TAG=$(git rev-parse HEAD)
    REGISTRY="acrQat.azurecr.io"
    DOCKER_FILE_DIR="."
    IMAGE_TAG="sysz-testing"
    read -p 'CONTAINER REGISTRY (default:acrQat.azurecr.io): ' REGISTRY
    read -p "IMAGE TAG (default: $GIT_TAG)": IMAGE_TAG
    if [ "$REGISTRY" = "" ]; then
        REGISTRY="acrQat.azurecr.io"
    fi
    if [ "$IMAGE_TAG" = "" ]; then
        IMAGE_TAG=$GIT_TAG
    fi
    if [ "$DOCKER_FILE_DIR" = "" ]; then
        DOCKER_FILE_DIR="."
    fi
    IMAGE_NAME="$REGISTRY/$SERVICE_NAME:$IMAGE_TAG"
    echo "Building image $IMAGE_NAME"
    docker build -t $IMAGE_NAME $DOCKER_FILE_DIR
    FINAL_IMAGE=$IMAGE_NAME
}

docker_push()
{
    if [ "$FINAL_IMAGE" = "" ]; then
        exit
    else
        echo "Pushing image $FINAL_IMAGE"
    fi
    PWD=$(pwd)
    docker push $FINAL_IMAGE
    IS_DEP=$(kubectl get deployments -o name | grep $SERVICE_NAME)
    if [[ "$IS_DEP" == "" ]]; then
        kubectl create -f $PWD/clusterinfo/deployment.yaml
    else
        kubectl set image deployment/$SERVICE_NAME $SERVICE_NAME=$FINAL_IMAGE
    fi
    HAS_SVC=$(kubectl get svc -o name | grep $SERVICE_NAME)
    if [[ "$HAS_SVC" == "" ]]; then
        kubectl create -f $PWD/clusterinfo/service.yaml
    fi
}


# Run the functions in this order
docker_build
docker_push
