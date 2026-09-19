#!/usr/bin/env bash

set -e
MAROONEDPODS_INSTALL_TIMEOUT=${MAROONEDPODS_INSTALL_TIMEOUT:-120}     #timeout for installation sequence

function install_maroonedpods {
  _kubectl apply -f "./_out/manifests/release/maroonedpods-operator.yaml"
}

function wait_maroonedpods_crd_installed {
  timeout=$1
  crd_defined=0
  while [ $crd_defined -eq 0 ] && [ $timeout > 0 ]; do
      crd_defined=$(_kubectl get customresourcedefinition| grep maroonedpods.io | wc -l)
      sleep 1
      timeout=$(($timeout-1))
  done

  #In case MAROONEDPODS crd is not defined after 120s - throw error
  if [ $crd_defined -eq 0 ]; then
     echo "ERROR - MAROONEDPODS CRD is not defined after timeout"
     exit 1
  fi  
}

function wait_mutating_webhook {
  timeout=${1:-180}
  while [ "$timeout" -gt 0 ]; do
    names="$(_kubectl get mutatingwebhookconfiguration maroonedpods-mutator -o jsonpath='{.webhooks[*].name}' 2>/dev/null || true)"
    if echo "$names" | grep -q gater; then
      echo "mutating webhook ready: ${names}"
      return 0
    fi
    sleep 2
    timeout=$((timeout-2))
  done
  echo "ERROR: maroonedpods-mutator has no webhook rules after timeout" >&2
  _kubectl get mutatingwebhookconfiguration maroonedpods-mutator -o yaml >&2 || true
  exit 1
}

