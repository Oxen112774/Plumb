package probe

import "testing"

// Real cgroup paths. These are not invented: the kubelet's two naming schemes
// (systemd slices and the cgroupfs driver's directories) and the runtime
// prefixes are the whole variety there is, and a parser tested against a
// tidied-up version of them will fail in production.
const (
	uidA = "8f2c1a3e-9d4b-4c6e-a1b2-c3d4e5f60718"
	// A real container ID is 64 hex characters. The parser refuses anything
	// else, so a fixture one character short would silently test the wrong
	// path — keep this exactly 64.
	cidA = "3d2e1f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5cd"
)

func TestParseK8sCgroupReadsSystemdContainerdPath(t *testing.T) {
	// systemd escapes the UID's hyphens as underscores, because a hyphen is
	// its field separator. A parser that does not undo that prints a UID no
	// kubectl command will match.
	path := "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-" +
		"pod8f2c1a3e_9d4b_4c6e_a1b2_c3d4e5f60718.slice/cri-containerd-" + cidA + ".scope"

	got := ParseK8sCgroup("0::" + path + "\n")
	if !got.InPod {
		t.Fatal("InPod = false for a kubepods path")
	}
	if got.PodUID != uidA {
		t.Errorf("PodUID = %q, want %q", got.PodUID, uidA)
	}
	if got.ContainerID != cidA {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, cidA)
	}
	if got.QOSClass != QoSBurstable {
		t.Errorf("QOSClass = %q, want Burstable", got.QOSClass)
	}
	if got.Runtime != "containerd" {
		t.Errorf("Runtime = %q, want containerd", got.Runtime)
	}
	if got.CgroupPath != path {
		t.Errorf("CgroupPath = %q, want the raw path so the parse can be checked", got.CgroupPath)
	}
}

// The cgroupfs driver uses plain directories with no runtime prefix, so the
// runtime cannot be named. Leaving it empty is the honest answer; guessing
// "docker" from the ID's shape would be a fabrication in a report.
func TestParseK8sCgroupReadsCgroupfsDriverPath(t *testing.T) {
	path := "/kubepods/burstable/pod" + uidA + "/" + cidA

	got := ParseK8sCgroup("0::" + path + "\n")
	if !got.InPod {
		t.Fatal("InPod = false for a cgroupfs kubepods path")
	}
	if got.PodUID != uidA {
		t.Errorf("PodUID = %q, want %q", got.PodUID, uidA)
	}
	if got.ContainerID != cidA {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, cidA)
	}
	if got.Runtime != "" {
		t.Errorf("Runtime = %q, want empty: the cgroupfs driver names no runtime", got.Runtime)
	}
	if got.QOSClass != QoSBurstable {
		t.Errorf("QOSClass = %q, want Burstable", got.QOSClass)
	}
}

func TestParseK8sCgroupReadsQoSClasses(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"besteffort", "/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" +
			uidA + ".slice/cri-containerd-" + cidA + ".scope", QoSBestEffort},
		{"burstable", "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" +
			uidA + ".slice/crio-" + cidA + ".scope", QoSBurstable},
		// Guaranteed pods get no class slice at all: the kubelet puts them
		// directly under kubepods. That absence is the signal.
		{"guaranteed", "/kubepods.slice/kubepods-pod" + uidA +
			".slice/cri-containerd-" + cidA + ".scope", QoSGuaranteed},
		{"cri-o runtime", "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" +
			uidA + ".slice/crio-" + cidA + ".scope", QoSBurstable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseK8sCgroup("0::" + tc.path + "\n")
			if got.QOSClass != tc.want {
				t.Errorf("QOSClass = %q, want %q", got.QOSClass, tc.want)
			}
		})
	}
}

func TestParseK8sCgroupNamesTheRuntimeFromTheLeafCgroup(t *testing.T) {
	cases := []struct{ leaf, want string }{
		{"cri-containerd-" + cidA, "containerd"},
		{"crio-" + cidA, "cri-o"},
		{"docker-" + cidA, "docker"},
		{"libpod-" + cidA, "podman"},
	}
	uid := "pod8f2c1a3e_9d4b_4c6e_a1b2_c3d4e5f60718"
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			path := "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-" +
				uid + ".slice/" + tc.leaf + ".scope"
			if got := ParseK8sCgroup("0::" + path + "\n"); got.Runtime != tc.want {
				t.Errorf("Runtime = %q, want %q", got.Runtime, tc.want)
			}
		})
	}
}

// A container that is not in Kubernetes must not be reported as a pod. A
// standalone docker container runs under /system.slice, and systemd user
// sessions run under /user.slice — both are containers or slices, neither is
// a pod, and printing one would send someone to look for a workload that has
// no name.
func TestParseK8sCgroupIgnoresNonKubernetesContainers(t *testing.T) {
	paths := []string{
		"0::/system.slice/docker-" + cidA + ".scope\n",
		"0::/user.slice/user-1000.slice/session-2.scope\n",
		"0::/\n",
		"0::/init.scope\n",
	}
	for _, p := range paths {
		if got := ParseK8sCgroup(p); got.InPod {
			t.Errorf("InPod = true for %q: that is not a kubelet path", p)
		}
	}
}

// On a hybrid system /proc/self/cgroup carries a line per v1 controller
// before the v2 line. The pod identity is on whichever line mentions
// kubepods, and it must be found rather than assumed to be the first one.
func TestParseK8sCgroupFindsThePodLineAmongSeveral(t *testing.T) {
	data := "12:pids:/kubepods/burstable/pod" + uidA + "/" + cidA + "\n" +
		"11:hugetlb:/kubepods/burstable/pod" + uidA + "/" + cidA + "\n" +
		"0::/user.slice/user-1000.slice/session-2.scope\n"

	got := ParseK8sCgroup(data)
	if !got.InPod {
		t.Fatal("InPod = false; the kubepods lines were not on the first line")
	}
	if got.PodUID != uidA {
		t.Errorf("PodUID = %q, want %q", got.PodUID, uidA)
	}
}

// A path component that merely starts with "pod" is not a pod UID. Without
// this check a pod named "podman-helper" would be reported with a UID of
// "man-helper".
func TestParseK8sCgroupRejectsNonUIDSegments(t *testing.T) {
	got := ParseK8sCgroup("0::/kubepods/burstable/podnotanuid/" + cidA + "\n")
	if !got.InPod {
		t.Fatal("InPod = false; the path is still under kubepods")
	}
	if got.PodUID != "" {
		t.Errorf("PodUID = %q, want empty: that segment is not a UID", got.PodUID)
	}
	if got.ContainerID != cidA {
		t.Errorf("ContainerID = %q, want %q — a bad UID must not lose the container ID", got.ContainerID, cidA)
	}
}

func TestIdentityRendersOneLine(t *testing.T) {
	got := ParseK8sCgroup("0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" +
		uidA + ".slice/cri-containerd-" + cidA + ".scope\n").Identity()
	// The UID is abbreviated: a full 36 characters would push the rest of a
	// header line off the screen, and eight is more than enough to tell two
	// snapshots apart.
	want := "pod 8f2c1a3e… · Burstable · containerd"
	if got != want {
		t.Errorf("Identity() = %q, want %q", got, want)
	}
	if ParseK8sCgroup("0::/\n").Identity() != "" {
		t.Error("Identity() is not empty outside a pod")
	}
}

func TestReadK8sContextReadsSelfCgroup(t *testing.T) {
	root := fixture(t, map[string]string{
		"proc/self/cgroup": "0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" +
			uidA + ".slice/cri-containerd-" + cidA + ".scope\n",
	})
	got := ReadK8sContext(root)
	if !got.InPod || got.PodUID != uidA {
		t.Errorf("ReadK8sContext = %+v, want the pod from the fixture", got)
	}
}

// A file that is not there is not an error. On bare metal there is no pod to
// name, and a report that complains about a missing Kubernetes is worse than
// one that simply does not mention it.
func TestReadK8sContextIsSilentWhenAbsent(t *testing.T) {
	if got := ReadK8sContext(fixture(t, map[string]string{"proc/stat": "cpu 1 2\n"})); got.InPod {
		t.Errorf("InPod = true with no /proc/self/cgroup: %+v", got)
	}
}
