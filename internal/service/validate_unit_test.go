package service

import (
	"strings"
	"testing"
)

// TestValidatorsReject exercises every rejection branch of the input
// validators directly, which is faster and more precise than driving each one
// through a public method.
func TestValidatorsReject(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"requireRef 空", refErr(""), "不能为空"},
		{"requireRef 首尾空白", refErr(" web"), "首尾不能有空白"},
		{"requireRef 前导横线", refErr("-web"), "'-'"},
		{"requireRef 内部空白", refErr("we b"), "空白"},
		{"requireRef 换行", refErr("we\nb"), "空白"},
		{"requireRef NUL", refErr("we\x00b"), "空白"},
		{"requireName 非法字符", nameErr("bad/name"), "非法字符"},
		{"requireName 前导符号", nameErr("-web"), "'-'"},
		{"requireName 过长", nameErr(strings.Repeat("a", 129)), "过长"},
		{"requireName 中文", nameErr("容器"), "非法字符"},
		{"requireText 空", textErr(""), "不能为空"},
		{"requireText 首尾空白", textErr(" x "), "首尾不能有空白"},
		{"requireText 前导横线", textErr("-x"), "'-'"},
		{"requireText 控制字符", textErr("a\r\nb"), "控制字符"},
		{"validateDriver 前导横线", validateDriverErr("-x"), "驱动名非法"},
		{"validateDriver 空白", validateDriverErr("br idge"), "驱动名非法"},
		{"validateTimeoutSec 负数", timeoutErr(-1), "不能为负数"},
		{"validateSignal 非法", signalErr("SIG KILL"), "信号非法"},
		{"validateSignal 分号", signalErr("SIG;KILL"), "信号非法"},
		{"validateMemory 非法", memErr("12X"), "内存限制格式非法"},
		{"validateMemory 空单位前空格", memErr("12 M"), "内存限制格式非法"},
		{"validateCPUs 非法", cpuErr("many"), "CPU 数量格式非法"},
		{"validateCPUs 负数", cpuErr("-1"), "CPU 数量格式非法"},
		{"validatePullPolicy 非法", pullErr("sometimes"), "--pull"},
		{"validateProgress 非法", progressErr("fancy"), "--progress"},
		{"validateKeyValue 空", kvErr(""), "不能为空"},
		{"validateKeyValue 前导横线", kvErr("-A=1"), "'-'"},
		{"validateKeyValue 缺少等号", kvErr("JUSTAKEY"), "KEY=VALUE"},
		{"validateKeyValue 空键", kvErr("=1"), "KEY=VALUE"},
		{"validateKeyValue 控制字符", kvErr("A=1\nB=2"), "控制字符"},
		{"validatePort 空", portErr(""), "不能为空"},
		{"validatePort 前导横线", portErr("-1:2"), "端口映射非法"},
		{"validatePort 无冒号", portErr("8080"), "host:container"},
		{"validatePort 容器侧为空", portErr("8080:"), "host:container"},
		{"validatePort 主机侧为空", portErr(":80"), "host:container"},
		{"validateMount 空", mountErr(""), "不能为空"},
		{"validateMount 前导横线", mountErr("-v:/d"), "卷映射非法"},
		{"validateMount 无冒号", mountErr("data"), "source:target"},
		{"validateMount 缺目标", mountErr("data:"), "source:target"},
		{"validateMount 尾冒号", mountErr("a:/b:"), "缺少容器内路径"},
		{"validateTimeValue 前导横线", timeErr("-1"), "非法"},
		{"validateTimeValue 含空格", timeErr("2024-01-15 10:30"), "非法"},
		{"validateSubnet 非 CIDR", subnetErr("172.20.0.0"), "CIDR"},
		{"validateSubnet 非 IP", subnetErr("banana/16"), "CIDR"},
		{"validateSubnet 前导横线", subnetErr("-172.20.0.0/16"), "子网非法"},
		{"validateGateway 非 IP", gatewayErr("not-an-ip"), "网关"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("该输入必须被拒绝")
			}
			if !strings.Contains(tc.err.Error(), tc.want) {
				t.Fatalf("错误应包含 %q，实际：%v", tc.want, tc.err)
			}
		})
	}
}

// TestValidatorsAcceptAndNormalize pins the accepted values, including the
// normalization (lower-casing, trimming) the command builders rely on.
func TestValidatorsAcceptAndNormalize(t *testing.T) {
	if err := refErr("web-1"); err != nil {
		t.Errorf("常规引用应通过：%v", err)
	}
	if err := refErr("a1b2c3d4e5f6"); err != nil {
		t.Errorf("ID 形式的引用应通过：%v", err)
	}
	if err := refErr("ghcr.io/ns/img:1.2"); err != nil {
		t.Errorf("镜像引用应通过：%v", err)
	}
	if err := nameErr("1web"); err != nil {
		t.Errorf("数字开头的名称应通过：%v", err)
	}
	if err := nameErr("a.b_c-1"); err != nil {
		t.Errorf("含 _ . - 的名称应通过：%v", err)
	}
	if err := nameErr(strings.Repeat("a", 128)); err != nil {
		t.Errorf("128 字符的名称应通过：%v", err)
	}
	if err := textErr("a b"); err != nil {
		t.Errorf("内部空格应允许：%v", err)
	}

	for _, memory := range []string{"512", "512M", "1G", "1.5GiB", "2g", "1024KB"} {
		if err := memErr(memory); err != nil {
			t.Errorf("内存 %q 应通过：%v", memory, err)
		}
	}
	for _, cpus := range []string{"0.5", "1", "2.5", "8"} {
		if err := cpuErr(cpus); err != nil {
			t.Errorf("CPU %q 应通过：%v", cpus, err)
		}
	}
	if got, err := validatePullPolicy(" ALWAYS "); err != nil || got != "always" {
		t.Errorf("pull 策略应归一化为小写，实际 %q/%v", got, err)
	}
	if got, err := validateProgress("Plain"); err != nil || got != "plain" {
		t.Errorf("progress 应归一化为小写，实际 %q/%v", got, err)
	}
	if got, err := validateDriver(""); err != nil || got != "" {
		t.Errorf("空驱动应被接受并返回空串，实际 %q/%v", got, err)
	}
	if got, err := validateSignal("  "); err != nil || got != "" {
		t.Errorf("空信号应被接受并返回空串，实际 %q/%v", got, err)
	}
	if got, err := validateTimeoutSec("超时", 0); err != nil || got != "" {
		t.Errorf("0 秒应表示默认超时（无 flag），实际 %q/%v", got, err)
	}
	if got, err := validateTimeoutSec("超时", 30); err != nil || got != "30" {
		t.Errorf("正数超时应转成字符串，实际 %q/%v", got, err)
	}
	for _, mount := range []string{"vol:/data", `C:\share:/mnt/share`, "vol:/data:ro", "./local:/app"} {
		if err := mountErr(mount); err != nil {
			t.Errorf("卷映射 %q 应通过：%v", mount, err)
		}
	}
	for _, port := range []string{"80:80", "8080:80", "127.0.0.1:8080:80", "80:80/udp"} {
		if err := portErr(port); err != nil {
			t.Errorf("端口映射 %q 应通过：%v", port, err)
		}
	}
	for _, ignored := range []string{"", "  "} {
		if err := timeErr(ignored); err != nil {
			t.Errorf("空 since/until 应被接受：%v", err)
		}
		if err := subnetErr(ignored); err != nil {
			t.Errorf("空子网应被接受：%v", err)
		}
		if err := gatewayErr(ignored); err != nil {
			t.Errorf("空网关应被接受：%v", err)
		}
	}
	if err := subnetErr("::/0"); err != nil {
		t.Errorf("IPv6 子网应通过：%v", err)
	}
	if err := gatewayErr("::1"); err != nil {
		t.Errorf("IPv6 网关应通过：%v", err)
	}
	if err := timeErr("2024-01-15T10:30:00Z"); err != nil {
		t.Errorf("RFC3339 时间应通过：%v", err)
	}
	if err := timeErr("1700000000"); err != nil {
		t.Errorf("Unix 时间戳应通过：%v", err)
	}
}

// TestValidateContainerFilter covers the filter validator directly.
func TestValidateContainerFilter(t *testing.T) {
	if err := validateContainerFilter(ContainerFilter{Limit: -1}); err == nil {
		t.Error("负数 Limit 应被拒绝")
	}
	for _, state := range containerStates {
		if err := validateContainerFilter(ContainerFilter{State: state}); err != nil {
			t.Errorf("状态 %q 应被接受：%v", state, err)
		}
		if err := validateContainerFilter(ContainerFilter{State: strings.ToUpper(state)}); err != nil {
			t.Errorf("状态 %q 大小写不敏感：%v", state, err)
		}
	}
	if err := validateContainerFilter(ContainerFilter{State: "bogus"}); err == nil {
		t.Error("未知状态应被拒绝")
	}
	if err := validateContainerFilter(ContainerFilter{}); err != nil {
		t.Errorf("空过滤器应被接受：%v", err)
	}
}

// --- tiny adapters so the table above stays readable -----------------------

func refErr(value string) error     { _, err := requireRef("引用", value); return err }
func nameErr(value string) error    { _, err := requireName("名称", value); return err }
func textErr(value string) error    { _, err := requireText("文本", value); return err }
func kvErr(value string) error      { _, err := validateKeyValue("键值", value); return err }
func portErr(value string) error    { _, err := validatePort(value); return err }
func mountErr(value string) error   { _, err := validateMount(value); return err }
func timeErr(value string) error    { _, err := validateTimeValue("--since", value); return err }
func subnetErr(value string) error  { _, err := validateSubnet(value); return err }
func gatewayErr(value string) error { _, err := validateGateway(value); return err }

func validateDriverErr(value string) error { _, err := validateDriver(value); return err }
func timeoutErr(seconds int) error         { _, err := validateTimeoutSec("超时", seconds); return err }
func signalErr(value string) error         { _, err := validateSignal(value); return err }
func memErr(value string) error            { return validateMemory(value) }
func cpuErr(value string) error            { return validateCPUs(value) }
func pullErr(value string) error           { _, err := validatePullPolicy(value); return err }
func progressErr(value string) error       { _, err := validateProgress(value); return err }
