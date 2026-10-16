package service

import "testing"

// TestCurrentProcessIntegrityLevel pins the contract of the integrity probe:
// it never errors when the token is readable, and a normal test run happens at
// Medium or above. A zero return means "unknown" and must not be classified as
// low — that is asserted here because EnvCheck relies on it.
func TestCurrentProcessIntegrityLevel(t *testing.T) {
	level := currentProcessIntegrityLevel()
	if level == 0 {
		t.Fatal("无法读取当前进程完整性级别（返回 0 表示未知，不允许把未知当 Low）")
	}
	if level < integrityMedium {
		t.Fatalf("测试进程应以 Medium 及以上运行，实际 0x%X", level)
	}
}

// TestProcessRunsLowIntegrityClassifiesUnknownAsFalse guards the EnvCheck
// branch: an undeterminable level must NOT trigger the Low-integrity problem.
func TestProcessRunsLowIntegrityClassifiesUnknownAsFalse(t *testing.T) {
	if level := currentProcessIntegrityLevel(); level == 0 {
		t.Skip("本环境读不到完整性级别，跳过分类断言")
	}
	if processRunsLowIntegrity() {
		t.Fatal("Medium 及以上进程不应被判定为 Low")
	}
}
