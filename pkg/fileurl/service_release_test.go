package fileurl

import (
	"strings"
	"testing"
)

func TestServiceDownloadOnlyUsesFork(t *testing.T) {
	for _, tag := range []string{"3.6.2", "v3.6.2", "v3.6.2-alpha.1"} {
		for _, platform := range [][2]string{{"linux", "amd64"}, {"linux", "arm"}, {"windows", "amd64"}} {
			u, name, err := ServiceDownload(tag, platform[0], platform[1])
			arch := platform[1]
			if arch == "arm" {
				arch = "armv7l"
			}
			expected := "fast-note-sync-service-" + tag + "-" + platform[0] + "-" + arch + ".tar.gz"
			if err != nil || name != expected || u != "https://github.com/liu-zhengdong/fast-note-sync-service/releases/download/"+tag+"/"+expected {
				t.Fatalf("%s: %s %s %v", tag, u, name, err)
			}
			if strings.Contains(u, "cnb.cool") || strings.Contains(u, "haierkeys") {
				t.Fatalf("official source used: %s", u)
			}
		}
	}
}

func TestServiceDownloadRejectsUnsafeTags(t *testing.T) {
	for _, tag := range []string{"", "latest", "1", "1.2", "v1.2.3/evil", "../../1.2.3", "1.2.3?url=https://cnb.cool", "1.2.3#x", "1.2.3%2f..", "1.2.3\n", "vv1.2.3", "01.2.3"} {
		if _, _, err := ServiceDownload(tag, "linux", "amd64"); err == nil {
			t.Errorf("accepted unsafe tag %q", tag)
		}
	}
	if _, _, err := ServiceDownload("v3.6.2", "linux", "386"); err == nil {
		t.Fatal("accepted platform with no published archive")
	}
}
