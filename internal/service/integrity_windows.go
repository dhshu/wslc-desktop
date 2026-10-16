package service

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Mandatory integrity levels from the Windows security model. A process whose
// token sits at Low cannot open service RPC endpoints, which is how a
// Low-labeled exe surfaced as 「拒绝访问」 on every wslc session command while
// `wslc version` (no RPC) kept working.
const (
	integrityLow    uint32 = 0x1000
	integrityMedium uint32 = 0x2000
)

// currentProcessIntegrityLevel returns the mandatory integrity level of the
// current process token, or 0 when it cannot be determined. Zero therefore
// means "unknown" and callers must not treat it as low.
func currentProcessIntegrityLevel() uint32 {
	token := windows.GetCurrentProcessToken()
	var returned uint32
	// Two-call pattern: the sizing call always fails with a "data area too
	// small" error while filling returned, so only returned matters here.
	_ = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &returned)
	if returned == 0 {
		return 0
	}
	buf := make([]byte, returned)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &returned); err != nil {
		return 0
	}
	// TOKEN_MANDATORY_LABEL = { PSID Sid; DWORD Attributes }: the first
	// pointer-sized word points at the label SID inside buf. SID layout is
	// revision(1) + subAuthorityCount(1) + identifierAuthority(6) +
	// subAuthorities(4 bytes each), with the RID last.
	sid := *(*unsafe.Pointer)(unsafe.Pointer(&buf[0]))
	if sid == nil {
		return 0
	}
	count := *(*uint8)(unsafe.Add(sid, 1))
	if count == 0 {
		return 0
	}
	return *(*uint32)(unsafe.Add(sid, uintptr(8+4*(count-1))))
}

// processRunsLowIntegrity reports whether this process is locked below Medium,
// which Windows punishes with E_ACCESSDENIED on cross-process service calls.
func processRunsLowIntegrity() bool {
	level := currentProcessIntegrityLevel()
	return level != 0 && level < integrityMedium
}
