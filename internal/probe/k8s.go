package probe

import (
	"strings"
)

// K8sContext is what plumb can establish about the Kubernetes pod it is
// running inside.
//
// It is read from /proc/self/cgroup — the one file where the kernel reports
// which cgroup this process actually sits in — and nothing else. The obvious
// alternatives all lie: the hostname is the pod name only until someone sets
// `hostname:` in the spec, /proc/1/cgroup describes init rather than this
// process, and the Downward API requires the pod to have been written with
// plumb in mind. A cgroup path is placed there by the kubelet itself.
//
// Every field is therefore either read from the kernel or empty. Nothing here
// is inferred from a convention, and nothing is filled in from an environment
// variable, because a guess that looks like a fact is the worst thing a
// diagnosis can carry.
type K8sContext struct {
	// InPod is true when this process's cgroup path is under kubepods, which
	// means a kubelet put it there.
	InPod bool `json:"in_pod"`

	// PodUID is the pod's UID as it appears in the cgroup path. It is the
	// identity that survives a restart — pod names do not — so it is the
	// right thing to print when twenty snapshots come back from twenty nodes
	// and have to be told apart.
	PodUID string `json:"pod_uid,omitempty"`

	// ContainerID is the runtime's container ID, which is what `crictl` and
	// `docker inspect` take. It is the last path component, stripped of the
	// runtime's prefix and the systemd suffix.
	ContainerID string `json:"container_id,omitempty"`

	// QOSClass is Guaranteed, Burstable or BestEffort, taken from the slice
	// the pod was placed in. It predicts how the pod dies under pressure:
	// BestEffort is evicted first and has no CPU quota at all.
	QOSClass string `json:"qos_class,omitempty"`

	// Runtime is the container runtime that named the leaf cgroup:
	// containerd, cri-o, docker or podman.
	Runtime string `json:"runtime,omitempty"`

	// CgroupPath is the raw path, carried so a reader can check the parse
	// rather than trust it. An identification that cannot be audited is
	// worth less than the three lines it saves.
	CgroupPath string `json:"cgroup_path,omitempty"`
}

// QoS classes, as the kubelet names them in the cgroup hierarchy.
const (
	QoSGuaranteed = "Guaranteed"
	QoSBurstable  = "Burstable"
	QoSBestEffort = "BestEffort"
)

// Identity renders the pod in one line for a report header, e.g.
// "pod 8f2c1a3e… · Burstable · containerd". Empty when not in a pod.
func (k K8sContext) Identity() string {
	if !k.InPod {
		return ""
	}
	parts := make([]string, 0, 3)
	if k.PodUID != "" {
		parts = append(parts, "pod "+shortUID(k.PodUID))
	}
	if k.QOSClass != "" {
		parts = append(parts, k.QOSClass)
	}
	if k.Runtime != "" {
		parts = append(parts, k.Runtime)
	}
	return strings.Join(parts, " · ")
}

// ReadK8sContext reads this process's cgroup path and identifies the pod.
//
// A missing or unrecognisable file leaves the zero value: on bare metal, and
// under a container runtime that is not Kubernetes, there is no pod to name,
// and saying so is better than printing a blank.
func ReadK8sContext(r Root) K8sContext {
	data, err := r.ReadFile("proc/self/cgroup")
	if err != nil {
		return K8sContext{}
	}
	return ParseK8sCgroup(string(data))
}

// ParseK8sCgroup identifies a pod from the contents of /proc/self/cgroup.
//
// The file is one line per hierarchy, "id:controllers:path"; under the unified
// hierarchy the controller list is empty, so the line is "0::/path". Several
// lines can appear on a hybrid system, and the one that identifies a pod is
// the one whose path is under kubepods — chosen rather than assumed to be
// first, because on such a system the v1 lines come first and say nothing.
func ParseK8sCgroup(data string) K8sContext {
	var best K8sContext
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Two colons: the middle field may legitimately be empty (v2), so
		// splitting on ":" and taking the remainder keeps the path intact
		// even when it contains none of the characters we look for next.
		_, path, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		_, path, ok = strings.Cut(path, ":")
		if !ok {
			continue
		}
		if ctx := parseCgroupPath(path); ctx.InPod {
			return ctx
		}
	}
	return best
}

// parseCgroupPath pulls pod identity out of one cgroup path.
func parseCgroupPath(path string) K8sContext {
	var ctx K8sContext
	if !strings.Contains(path, "kubepods") {
		return ctx
	}
	ctx.InPod = true
	ctx.CgroupPath = path

	segs := strings.Split(strings.Trim(path, "/"), "/")
	for _, seg := range segs {
		slice := strings.TrimSuffix(seg, ".slice")
		switch {
		case strings.Contains(slice, "besteffort"):
			ctx.QOSClass = QoSBestEffort
		case strings.Contains(slice, "burstable"):
			ctx.QOSClass = QoSBurstable
		}
		if ctx.PodUID == "" {
			if uid, ok := podUIDFromSegment(slice); ok {
				ctx.PodUID = uid
			}
		}
		if id, runtime, ok := containerFromSegment(slice); ok {
			ctx.ContainerID, ctx.Runtime = id, runtime
		}
	}
	// A pod under kubepods with no burstable or besteffort slice is
	// Guaranteed: that is where the kubelet puts pods whose requests equal
	// their limits, and there is no slice named after the class.
	if ctx.QOSClass == "" {
		ctx.QOSClass = QoSGuaranteed
	}
	return ctx
}

// podUIDFromSegment reads a pod UID out of either naming scheme.
//
// systemd writes "kubepods-burstable-pod<uid>.slice" and escapes the UID's
// hyphens as underscores, because a hyphen separates name fields; the
// cgroupfs driver writes a plain "pod<uid>". Both are read, and the escapes
// are undone, so the printed UID matches what kubectl reports.
func podUIDFromSegment(seg string) (string, bool) {
	// systemd joins fields with hyphens, so the pod field opens with "-pod";
	// the cgroupfs driver uses directories, where it is the whole name.
	var rest string
	if i := strings.Index(seg, "-pod"); i >= 0 {
		rest = seg[i+len("-pod"):]
	} else if strings.HasPrefix(seg, "pod") {
		rest = strings.TrimPrefix(seg, "pod")
	} else {
		return "", false
	}
	rest = strings.TrimSuffix(rest, ".slice")
	rest = strings.TrimSuffix(rest, ".scope")
	// systemd escapes the UID's hyphens as underscores, since a hyphen is
	// its field separator; undoing that makes the printed UID the same
	// string kubectl shows.
	uid := strings.ReplaceAll(rest, "_", "-")
	if !looksLikeUID(uid) {
		return "", false
	}
	return uid, true
}

// containerFromSegment reads the container ID and the runtime that named it.
//
// The runtime prefixes its own name on the leaf cgroup under the systemd
// driver ("cri-containerd-<id>", "crio-<id>", "docker-<id>"); under the
// cgroupfs driver the leaf is the bare ID, and the runtime then cannot be
// named — which is stated as an unknown rather than guessed from the ID.
func containerFromSegment(seg string) (id, runtime string, ok bool) {
	for _, p := range []struct{ prefix, name string }{
		{"cri-containerd-", "containerd"},
		{"crio-", "cri-o"},
		{"docker-", "docker"},
		{"libpod-", "podman"},
	} {
		if s := strings.TrimPrefix(seg, p.prefix); s != seg {
			id = strings.TrimSuffix(s, ".scope")
			if looksLikeContainerID(id) {
				return id, p.name, true
			}
			// An ID that is not 64 hex characters is not a container ID;
			// naming it anyway would put a wrong string in a report.
			return "", "", false
		}
	}
	if looksLikeContainerID(seg) {
		return seg, "", true
	}
	return "", "", false
}

// looksLikeUID reports whether s could be a Kubernetes UID: a UUID, which is
// 36 characters of hex and hyphens.
func looksLikeUID(s string) bool {
	if len(s) < 32 || len(s) > 40 {
		return false
	}
	hyphens := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		case c == '-':
			hyphens++
		default:
			return false
		}
	}
	return hyphens >= 4
}

// looksLikeContainerID reports whether s is a 64-character hex digest, the
// shape every runtime's container ID has.
func looksLikeContainerID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// shortUID abbreviates a UID for a header, where the full 36 characters would
// push the rest of the line off the screen. Eight characters is what kubectl
// itself shows for a pod suffix, and is far more than is needed to tell two
// snapshots apart.
func shortUID(uid string) string {
	if len(uid) <= 8 {
		return uid
	}
	return uid[:8] + "…"
}
