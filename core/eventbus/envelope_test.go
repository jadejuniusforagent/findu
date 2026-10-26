package eventbus

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

type contractSchema struct {
	Properties struct {
		Type struct {
			Enum []string `json:"enum"`
		} `json:"type"`
	} `json:"properties"`
}

// 契约测试：Go 侧 Type 常量必须与 event-contract.schema.json 的 enum 双向一致。
// 少一个 → 插件事件被误拒；多一个 → 接受了契约未定义的事件。
func TestTypesMatchContract(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/event-contract.schema.json")
	if err != nil {
		t.Fatalf("读取契约: %v", err)
	}
	var schema contractSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("解析契约: %v", err)
	}
	want := map[string]bool{}
	for _, s := range schema.Properties.Type.Enum {
		want[s] = true
	}
	got := map[string]bool{}
	for _, tp := range AllTypes() {
		if got[string(tp)] {
			t.Fatalf("Go 侧类型重复: %s", tp)
		}
		got[string(tp)] = true
		if !want[string(tp)] {
			t.Fatalf("Go 类型 %q 不在契约 enum 中", tp)
		}
	}
	for s := range want {
		if !got[s] {
			t.Fatalf("契约类型 %q 未在 Go 侧定义", s)
		}
	}
}

func TestEnvelopeValidate(t *testing.T) {
	now := time.Now().UTC()
	valid := Envelope{ID: "e1", Type: TypeC2SessionOpened, TS: now, Seq: 1, Delivery: AtLeastOnce}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法事件: %v", err)
	}
	// delivery 为空走默认值，Validate 应接受。
	valid.Delivery = ""
	if err := valid.Validate(); err != nil {
		t.Fatalf("空 delivery 应接受: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Envelope)
		is   error
	}{
		{"空 id", func(e *Envelope) { e.ID = "" }, ErrInvalidEnvelope},
		{"未知类型", func(e *Envelope) { e.Type = "nope" }, ErrUnknownType},
		{"零 ts", func(e *Envelope) { e.TS = time.Time{} }, ErrInvalidEnvelope},
		{"负 seq", func(e *Envelope) { e.Seq = -1 }, ErrInvalidEnvelope},
		{"非法 delivery", func(e *Envelope) { e.Delivery = "best_effort" }, ErrInvalidEnvelope},
	}
	for _, c := range cases {
		e := valid
		c.mut(&e)
		err := e.Validate()
		if err == nil || !errors.Is(err, c.is) {
			t.Fatalf("%s: err=%v want=%v", c.name, err, c.is)
		}
	}
}

func TestTypeAndDeliveryValid(t *testing.T) {
	if !TypeSessionCreated.Valid() || !TypeC2TaskCompleted.Valid() {
		t.Fatal("已知类型应合法")
	}
	if Type("session.created.extra").Valid() {
		t.Fatal("未知类型不应合法")
	}
	if !AtLeastOnce.Valid() || !AtMostOnce.Valid() {
		t.Fatal("已知 delivery 应合法")
	}
	if Delivery("x").Valid() {
		t.Fatal("未知 delivery 不应合法")
	}
	if len(AllTypes()) != 15 {
		t.Fatalf("AllTypes=%d", len(AllTypes()))
	}
	// 返回副本：修改不应影响内部表。
	types := AllTypes()
	types[0] = "mutated"
	if AllTypes()[0] == "mutated" {
		t.Fatal("AllTypes 必须返回副本")
	}
}
