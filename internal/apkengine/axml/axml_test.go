package axml

// AXML 解码验证:真实 APK(jadx 测试样本 app-with-fake-dex.apk)与
// androguard 语义基线(manifest_golden.json)比对。

import (
	"encoding/json"
	"os"
	"testing"

	"finduai/internal/apkengine/zipprobe"
)

const sampleApk = "testdata/app-with-fake-dex.apk"

func loadManifestRoot(t *testing.T) *Element {
	t.Helper()
	r, err := zipprobe.Open(sampleApk)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := zipprobe.ReadFile(r, "AndroidManifest.xml")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	root, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return root
}

func TestDecodeAgainstAndroguardGolden(t *testing.T) {
	goldenRaw, err := os.ReadFile("testdata/manifest_golden.json")
	if err != nil {
		t.Skip("manifest_golden.json missing")
	}
	var golden struct {
		Package     string   `json:"package"`
		VersionName string   `json:"version_name"`
		Activities  []string `json:"activities"`
	}
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatal(err)
	}

	root := loadManifestRoot(t)
	if root.Name != "manifest" {
		t.Fatalf("root = %s, want manifest", root.Name)
	}
	pkg, _ := root.Attr("package")
	if pkg != golden.Package {
		t.Errorf("package = %q, want %q", pkg, golden.Package)
	}
	versionName, _ := root.Attr("versionName")
	if versionName != golden.VersionName {
		t.Errorf("versionName = %q, want %q", versionName, golden.VersionName)
	}

	var activities []string
	for _, el := range root.FindAll("activity") {
		if name, ok := el.Attr("name"); ok {
			activities = append(activities, name)
		}
	}
	if len(activities) != len(golden.Activities) {
		t.Fatalf("activities = %v, want %v", activities, golden.Activities)
	}
	for i := range activities {
		if activities[i] != golden.Activities[i] {
			t.Errorf("activity[%d] = %q, want %q", i, activities[i], golden.Activities[i])
		}
	}
}

func TestDecodeUsesSDK(t *testing.T) {
	root := loadManifestRoot(t)
	usesSDK := root.FindAll("uses-sdk")
	if len(usesSDK) != 1 {
		t.Fatalf("uses-sdk = %d, want 1", len(usesSDK))
	}
	if minSdk, _ := usesSDK[0].Attr("minSdkVersion"); minSdk != "15" {
		t.Errorf("minSdkVersion = %q, want 15", minSdk)
	}
}

// 权限/组件查询的鲁棒性:无权限样本不 panic,返回空集。
func TestDecodeNoPermissions(t *testing.T) {
	root := loadManifestRoot(t)
	if perms := root.FindAll("uses-permission"); len(perms) != 0 {
		t.Errorf("uses-permission = %d, want 0", len(perms))
	}
}
