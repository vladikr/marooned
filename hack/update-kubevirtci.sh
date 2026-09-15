#!/bin/sh
#
# Copyright 2023 The MaroonedPods Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

SCRIPT_ROOT="$(cd "$(dirname $0)/../" && pwd -P)"

# the kubevirtci tag to vendor from (https://github.com/kubevirt/kubevirtci/tags)
# prow latest: https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirtci/latest
kubevirtci_release_tag=2609091017-ad4877a9

# remove previous cluster-up dir entirely before vendoring
rm -rf ${SCRIPT_ROOT}/cluster-up

# download and extract the cluster-up dir from a specific hash in kubevirtci
curl --fail -L "https://github.com/kubevirt/kubevirtci/archive/refs/tags/${kubevirtci_release_tag}.tar.gz" \
  | tar xz "kubevirtci-${kubevirtci_release_tag}/cluster-up" --strip-component 1

echo "KUBEVIRTCI_TAG=${kubevirtci_release_tag}" >>${SCRIPT_ROOT}/cluster-up/hack/common.sh

cat << 'EOF' >> ${SCRIPT_ROOT}/cluster-up/up.sh

# Rootless podman: gocli's iptables-legacy MASQUERADE often fails (ip_tables
# modprobe not permitted). Without it, nodes cannot pull from quay.io.
ensure_vm_nat() {
    local ctr="${KUBEVIRT_PROVIDER}-dnsmasq"
    local cri=podman
    if [ "${KUBEVIRTCI_RUNTIME:-}" = docker ]; then
        cri=docker
    fi
    echo "Ensuring MASQUERADE on ${ctr} for VM outbound NAT"
    ${cri} exec "${ctr}" sh -c '
        sysctl -w net.ipv4.ip_forward=1 >/dev/null
        iptables -t nat -C POSTROUTING -o eth0 -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
        iptables -C FORWARD -i br0 -o eth0 -j ACCEPT 2>/dev/null || iptables -A FORWARD -i br0 -o eth0 -j ACCEPT
    ' || echo "warning: could not add MASQUERADE on ${ctr}"
}
ensure_vm_nat

kubectl() { ${KUBEVIRTCI_PATH}/kubectl.sh "$@"; }

if [ "$KUBEVIRT_RELEASE" = "latest_nightly" ]; then
  LATEST=$(curl -L https://storage.googleapis.com/kubevirt-prow/devel/nightly/release/kubevirt/kubevirt/latest)
  kubectl apply -f https://storage.googleapis.com/kubevirt-prow/devel/nightly/release/kubevirt/kubevirt/${LATEST}/kubevirt-operator.yaml
  kubectl apply -f https://storage.googleapis.com/kubevirt-prow/devel/nightly/release/kubevirt/kubevirt/${LATEST}/kubevirt-cr.yaml
elif [ "$KUBEVIRT_RELEASE" = "latest_stable" ]; then
  RELEASE=$(curl https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)
  kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${RELEASE}/kubevirt-operator.yaml
  kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${RELEASE}/kubevirt-cr.yaml
else
  kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_RELEASE}/kubevirt-operator.yaml
  kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_RELEASE}/kubevirt-cr.yaml
fi
# Ensure the KubeVirt CRD is created
count=0
until kubectl get crd kubevirts.kubevirt.io; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CRD not found" && exit 1
    echo "waiting for KubeVirt CRD"
    sleep 1
done

# Ensure the KubeVirt API is available
count=0
until kubectl api-resources --api-group=kubevirt.io | grep kubevirts; do
    ((count++)) && ((count == 30)) && echo "KubeVirt API not found" && exit 1
    echo "waiting for KubeVirt API"
    sleep 1
done


# Ensure the KubeVirt CR is created
count=0
until kubectl -n kubevirt get kv kubevirt; do
    ((count++)) && ((count == 30)) && echo "KubeVirt CR not found" && exit 1
    echo "waiting for KubeVirt CR"
    sleep 1
done

# Wait until KubeVirt is ready
count=0
until kubectl wait -n kubevirt kv kubevirt --for condition=Available --timeout 5m; do
    ((count++)) && ((count == 5)) && echo "KubeVirt not ready in time" && exit 1
    echo "Error waiting for KubeVirt to be Available, sleeping 1m and retrying"
    sleep 1m
done
EOF

