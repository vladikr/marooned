package vsock

import "hash/fnv"

// CIDForUID picks a guest CID from a Pod UID. CIDs 0–2 are reserved
// (hypervisor, local, host). virt-handler is not in the one-pod path,
// so this is the allocator; stamp the value on VMI.Status.VSOCKCID.
func CIDForUID(uid string) uint32 {
	if uid == "" {
		return 3
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	cid := h.Sum32()
	if cid < 3 {
		cid += 3
	}
	return cid
}
