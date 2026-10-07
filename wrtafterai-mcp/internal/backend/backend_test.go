package backend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// withFakeExec 替换 execRunner，并记录调用参数。
// 返回 getter 而非切片本身：append 扩容后外部才能看到全部记录。
func withFakeExec(t *testing.T, fn func(name string, args []string) ([]byte, error)) func() []call {
	t.Helper()
	old := execRunner
	calls := []call{}
	execRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, call{name: name, args: args})
		return fn(name, args)
	}
	t.Cleanup(func() { execRunner = old })
	return func() []call { return calls }
}

type call struct {
	name string
	args []string
}

func TestUbusCallParsesJSON(t *testing.T) {
	withFakeExec(t, func(name string, args []string) ([]byte, error) {
		return []byte(`{"uptime":7200,"memory":{"total":262144,"free":131072,"cached":8192},"load":[0.1,0.2,0.3]}`), nil
	})

	info, err := GetSystemInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemInfo: %v", err)
	}
	if info.Uptime != 7200 {
		t.Errorf("Uptime = %d, want 7200", info.Uptime)
	}
	if info.Memory.Total != 262144 || info.Memory.Free != 131072 {
		t.Errorf("memory wrong: %+v", info.Memory)
	}
	if len(info.Load) != 3 || info.Load[2] != 0.3 {
		t.Errorf("load wrong: %v", info.Load)
	}
}

func TestUbusCallBuildsArgsWithParams(t *testing.T) {
	callsFn := withFakeExec(t, func(name string, args []string) ([]byte, error) {
		return []byte(`{"results":[{"mac":"aa:bb:cc:dd:ee:ff","signal":-50,"noise":-95,"rx":{"rate":130000},"tx":{"rate":260000}}]}`), nil
	})

	var out struct {
		Results []struct {
			Mac    string `json:"mac"`
			Signal int    `json:"signal"`
			Noise  int    `json:"noise"`
			Rx     struct {
				Rate uint64 `json:"rate"`
			} `json:"rx"`
			Tx struct {
				Rate uint64 `json:"rate"`
			} `json:"tx"`
		} `json:"results"`
	}
	err := UbusCall(context.Background(), "iwinfo", "assoclist", map[string]string{"device": "wlan0"}, &out)
	if err != nil {
		t.Fatalf("UbusCall: %v", err)
	}
	calls := callsFn()
	if len(calls) != 1 || calls[0].name != "ubus" {
		t.Fatalf("expected one ubus call, got %+v", calls)
	}
	args := calls[0].args
	if len(args) != 4 || args[0] != "call" || args[1] != "iwinfo" || args[2] != "assoclist" {
		t.Fatalf("args wrong: %v", args)
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(args[3]), &params); err != nil || params["device"] != "wlan0" {
		t.Fatalf("params wrong: %q err=%v", args[3], err)
	}
	if len(out.Results) != 1 || out.Results[0].Mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("results wrong: %+v", out.Results)
	}
	if out.Results[0].Rx.Rate != 130000 {
		t.Fatalf("rx rate wrong: %d", out.Results[0].Rx.Rate)
	}
}

func TestUbusCallErrorPropagates(t *testing.T) {
	withFakeExec(t, func(name string, args []string) ([]byte, error) {
		return nil, errors.New("exit status 1: Object not found")
	})

	var st struct{}
	if err := UbusCall(context.Background(), "network.interface.wan", "status", nil, &st); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestUbusCallNilParamsOmitted(t *testing.T) {
	callsFn := withFakeExec(t, func(name string, args []string) ([]byte, error) {
		return []byte(`{}`), nil
	})
	if err := UbusCall(context.Background(), "system", "board", nil, nil); err != nil {
		t.Fatal(err)
	}
	calls := callsFn()
	if len(calls) != 1 || len(calls[0].args) != 3 {
		t.Fatalf("nil params must omit 4th arg, got %+v", calls)
	}
}

func TestRunPropagatesTimeoutContext(t *testing.T) {
	withFakeExec(t, func(name string, args []string) ([]byte, error) {
		return []byte("ok"), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := run(ctx, 5*time.Second, "echo", "x")
	if err != nil || s != "ok" {
		t.Fatalf("run = %q, %v", s, err)
	}
}
