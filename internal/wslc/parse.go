package wslc

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
)

// Header spellings wslc 3.0.1 can print. The English and simplified-Chinese
// spellings were read out of the installed wslc.exe string table (its table
// headers are localized, so a zh-CN Windows prints 容器 ID / 状态 / … instead of
// CONTAINER ID / STATUS); the remaining entries cover the other locales wslc
// ships.
var (
	idAliases          = []string{"ID", "CONTAINER ID", "CONTAINER-ID", "容器 ID", "容器識別碼", "コンテナー ID", "컨테이너 ID", "CONTAINERIDENTIFIER"}
	nameAliases        = []string{"NAME", "NAMES", "名称", "名稱", "名前", "이름", "NAAM", "NAVN", "NOM", "NOMBRE", "NAZWA", "JMÉNO", "NOME", "ADLAR", "NEVEK", "NIMET"}
	imageAliases       = []string{"IMAGE", "映像", "影像", "画像", "BILD", "BILLEDE", "AFBEELDING", "IMMAGINE", "IMAGEM", "IMAGEN", "OBRAZ", "RESİM", "KÉP", "KUVA", "BILDE", "ОБРАЗ"}
	imageIDAliases     = []string{"IMAGE ID", "IMAGE-ID", "映像 ID", "影像識別碼", "画像 ID", "이미지 ID"}
	commandAliases     = []string{"COMMAND", "命令", "コマンド", "명령", "BEFEHL", "KOMMANDO", "OPDRACHT", "COMMANDE", "COMANDO", "POLECENIE", "KOMUT", "PARANCS", "KOMENTO"}
	createdAliases     = []string{"CREATED", "CREATED AT", "已创建", "已建立", "作成済み", "생성됨", "ERSTELLT", "OPRETTET", "GEMAAKT", "DATE DE CRÉATION", "CREATO", "CRIADO", "CREADO", "UTWORZONO", "OLUŞTURULDU", "LUOTU", "OPPRETTET", "VYTVOŘENO", "SKAPADES", "VYTVOŘEN"}
	runningForAliases  = []string{"RUNNING FOR", "RUNNINGFOR", "运行时间"}
	statusAliases      = []string{"STATUS", "状态", "狀態", "状態", "상태", "ÉTAT", "STAV", "STATO", "ESTADO", "DURUM", "STAN", "TILA", "СТАТУС"}
	stateAliases       = []string{"STATE"}
	portsAliases       = []string{"PORTS", "PORT", "端口", "連接埠", "ポート", "포트", "PORTE", "POORTEN", "PORTAS", "PORTAR", "PORTY", "PUERTOS", "PORTOK", "PORTIT", "PORTER", "ПОРТЫ", "BAĞLANTI NOKTALARI"}
	sizeAliases        = []string{"SIZE", "大小", "サイズ", "크기", "GRÖSSE", "STØRRELSE", "GROOTTE", "TAILLE", "DIMENSIONI", "TAMANHO", "TAMAÑO", "ROZMIAR", "BOYUT", "KOKO", "VELIKOST", "STORLEK", "РАЗМЕР"}
	containerLabels    = []string{"LABELS", "LABEL", "标签", "標籤", "ラベル", "레이블", "ETIQUETAS"}
	mountsAliases      = []string{"MOUNTS", "MOUNT", "挂载", "掛載", "マウント", "마운트"}
	networksAliases    = []string{"NETWORKS", "NETWORK", "网络", "網路", "ネットワーク", "네트워크"}
	repositoryAliases  = []string{"REPOSITORY", "REPO", "存储库", "存儲庫", "存放庫", "リポジトリ", "리포지토리", "REPOSITORIO", "REPOSITÓRIO"}
	tagAliases         = []string{"TAG", "TAGS", "标记", "標籤", "标签", "タグ", "태그", "ETIQUETA", "MARCA"}
	digestAliases      = []string{"DIGEST", "摘要", "ダイジェスト", "다이제스트", "RESUMEN"}
	volumeNameAliases  = []string{"VOLUME NAME", "VOLUMENAME", "NAME", "卷名称", "磁碟區名稱", "ボリューム名", "볼륨 이름", "VOLUMENNAME", "NOM DU VOLUME"}
	driverAliases      = []string{"DRIVER", "驱动程序", "驅動程式", "ドライバー", "드라이버", "TREIBER", "STUURPROGRAMMA", "PILOTE", "СТУРПРОГРАММА"}
	scopeAliases       = []string{"SCOPE", "范围", "範圍", "スコープ", "범위", "PORTÉE", "ÁMBITO", "ZAKRES"}
	mountpointAliases  = []string{"MOUNTPOINT", "MOUNT POINT", "装入点", "挂载点", "マウントポイント", "탑재 지점"}
	networkIDAliases   = []string{"NETWORK ID", "NETWORK-ID", "网络 ID", "網路 ID", "ネットワーク ID", "네트워크 ID"}
	ipv6Aliases        = []string{"IPV6", "IPV6 ADDRESS", "IPV6 地址"}
	internalAliases    = []string{"INTERNAL", "内部", "INTERNE"}
	containersAliases  = []string{"CONTAINERS", "CONTAINER", "容器"}
	cpuAliases         = []string{"CPU %", "CPU%", "% CPU", "CPU 百分比", "CPU百分比", "CPU PERCENTAGE", "ЦП %"}
	memUsageAliases    = []string{"MEM USAGE / LIMIT", "MEM USAGE/LIMIT", "最大用量/限制", "記憶體使用量 / 限制", "メモリ使用量/上限", "MEM-NUTZUNG / -LIMIT", "UTILISATION/LIMITE MEM", "USO DE MEMORIA/LÍMITE", "UŻYCIE PAMIĘCI / LIMIT"}
	memPercAliases     = []string{"MEM %", "MEM%", "% MEM", "内存百分比", "記憶體 %", "メモリ使用率", "HUK %", "PAM %", "MUISTI %"}
	netIOAliases       = []string{"NET I/O", "网络 I/O", "網路 I/O", "ネットワーク I/O", "NETTO I/O", "NET-E/A", "NET G/Ç", "E/S DE RED", "VERKON I/O"}
	blockIOAliases     = []string{"BLOCK I/O", "块 I/O", "區塊 I/O", "ブロック I/O", "BLOCK-E/A", "BLOK G/Ç", "E/S DE BLOQUE", "VSTUP A VÝSTUP BLOKU", "BLOKOWE WE/WY"}
	pidsAliases        = []string{"PIDS", "PID", "PID'LER", "PID-TUNNUKSET"}
	creatorPIDAliases  = []string{"CREATOR PID", "CREATORPID", "创建者 PID", "建立者 PID", "作成者 PID", "작성자 PID", "ERSTELLER-PID", "FORFATTER-PID", "PID VAN MAKER", "PID DU CRÉATEUR", "PID CREATORE", "PID DO CRIADOR", "SKAPAR-PID", "PID AUTORA", "PID DE CREADOR", "IDENTYFIKATOR PID TWÓRCY", "OLUŞTURAN PID"}
	displayNameAliases = []string{"DISPLAY NAME", "NAME", "显示名称", "顯示名稱", "表示名", "표시 이름", "ANZEIGENAME", "VIST NAVN", "WEERGAVENAAM", "NOM COMPLET", "NOME VISUALIZZATO", "NOME DE EXIBIÇÃO", "VISNINGSNAMN"}
)

var (
	containerColumns = columnIndex([]columnSpec{
		{field: "id", aliases: idAliases},
		{field: "names", aliases: nameAliases},
		{field: "image", aliases: imageAliases},
		{field: "imageid", aliases: imageIDAliases},
		{field: "command", aliases: commandAliases},
		{field: "created", aliases: createdAliases},
		{field: "runningfor", aliases: runningForAliases},
		{field: "status", aliases: statusAliases},
		{field: "state", aliases: stateAliases},
		{field: "ports", aliases: portsAliases},
		{field: "size", aliases: sizeAliases},
		{field: "labels", aliases: containerLabels},
		{field: "mounts", aliases: mountsAliases},
		{field: "networks", aliases: networksAliases},
	})

	imageColumns = columnIndex([]columnSpec{
		{field: "repository", aliases: repositoryAliases},
		{field: "tag", aliases: tagAliases},
		{field: "digest", aliases: digestAliases},
		{field: "id", aliases: imageIDAliases},
		{field: "created", aliases: createdAliases},
		{field: "size", aliases: sizeAliases},
		{field: "labels", aliases: []string{"LABELS", "LABEL"}},
	})

	volumeColumns = columnIndex([]columnSpec{
		{field: "name", aliases: volumeNameAliases},
		{field: "driver", aliases: driverAliases},
		{field: "mountpoint", aliases: mountpointAliases},
		{field: "scope", aliases: scopeAliases},
		{field: "created", aliases: createdAliases},
		{field: "size", aliases: sizeAliases},
		{field: "labels", aliases: containerLabels},
	})

	networkColumns = columnIndex([]columnSpec{
		{field: "id", aliases: networkIDAliases},
		{field: "name", aliases: nameAliases},
		{field: "driver", aliases: driverAliases},
		{field: "scope", aliases: scopeAliases},
		{field: "ipv6", aliases: ipv6Aliases},
		{field: "internal", aliases: internalAliases},
		{field: "containers", aliases: containersAliases},
		{field: "created", aliases: createdAliases},
		{field: "labels", aliases: containerLabels},
	})

	statsColumns = columnIndex([]columnSpec{
		{field: "id", aliases: idAliases},
		{field: "name", aliases: nameAliases},
		{field: "cpuperc", aliases: cpuAliases},
		{field: "memusage", aliases: memUsageAliases},
		{field: "memperc", aliases: memPercAliases},
		{field: "netio", aliases: netIOAliases},
		{field: "blockio", aliases: blockIOAliases},
		{field: "pids", aliases: pidsAliases},
	})

	sessionColumns = columnIndex([]columnSpec{
		{field: "id", aliases: idAliases},
		{field: "creatorpid", aliases: creatorPIDAliases},
		{field: "name", aliases: displayNameAliases},
	})
)

// infoLabels maps the key/value form of `wslc info` (default table format) onto
// SystemInfo fields.
var infoLabels = map[string]string{
	normalizeColumn("WSL version"):             "version",
	normalizeColumn("WSL 版本"):                  "version",
	normalizeColumn("WSLバージョン"):                "version",
	normalizeColumn("Kernel version"):          "kernel",
	normalizeColumn("内核版本"):                    "kernel",
	normalizeColumn("Kernel 版本"):               "kernel",
	normalizeColumn("カーネルバージョン"):               "kernel",
	normalizeColumn("Direct3D version"):        "direct3d",
	normalizeColumn("Direct3D 版本"):             "direct3d",
	normalizeColumn("Direct3Dバージョン"):           "direct3d",
	normalizeColumn("DXCore version"):          "dxcore",
	normalizeColumn("DXCore 版本"):               "dxcore",
	normalizeColumn("DXCoreバージョン"):             "dxcore",
	normalizeColumn("Windows version"):         "windows",
	normalizeColumn("Windows 版本"):              "windows",
	normalizeColumn("Windowsバージョン"):            "windows",
	normalizeColumn("Settings file"):           "settingsfile",
	normalizeColumn("设置文件"):                    "settingsfile",
	normalizeColumn("設定ファイル"):                  "settingsfile",
	normalizeColumn("設定檔案"):                    "settingsfile",
	normalizeColumn("Session manager version"): "sessionmanager",
	normalizeColumn("会话管理器版本"):                 "sessionmanager",
	normalizeColumn("工作階段管理員版本"):               "sessionmanager",
	normalizeColumn("セッションマネージャーのバージョン"):       "sessionmanager",
}

// ParseContainers parses `wslc container list` output (JSON or table).
func ParseContainers(stdout string) ([]domain.Container, error) {
	if items, ok := jsonList[domain.Container](stdout, "containers", "container"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "container list", containerColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Container, 0, len(rows))
	for _, row := range rows {
		c := domain.Container{
			ID:      row["id"],
			Image:   row["image"],
			ImageID: row["imageid"],
			Command: row["command"],
			Status:  row["status"],
			State:   row["state"],
			Size:    row["size"],
			Labels:  row["labels"],
			Mounts:  row["mounts"],
		}
		switch {
		case row["runningfor"] != "":
			c.RunningFor = row["runningfor"]
		case row["created"] != "":
			if isAbsoluteTime(row["created"]) {
				c.CreatedAt = row["created"]
			} else {
				c.RunningFor = row["created"]
			}
		}
		c.Names = splitList(row["names"])
		c.Ports = splitList(row["ports"])
		c.Networks = splitList(row["networks"])
		out = append(out, c)
	}
	return out, nil
}

// ParseImages parses `wslc image list` output (JSON or table).
func ParseImages(stdout string) ([]domain.Image, error) {
	if items, ok := jsonList[domain.Image](stdout, "images", "image"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "image list", imageColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Image, 0, len(rows))
	for _, row := range rows {
		img := domain.Image{
			ID:         row["id"],
			Repository: row["repository"],
			Tag:        row["tag"],
			Digest:     row["digest"],
			Size:       row["size"],
			Labels:     row["labels"],
		}
		if created := row["created"]; created != "" {
			if isAbsoluteTime(created) {
				img.CreatedAt = created
			} else {
				img.CreatedSince = created
			}
		}
		out = append(out, img)
	}
	return out, nil
}

// ParseVolumes parses `wslc volume list` output (JSON or table).
func ParseVolumes(stdout string) ([]domain.Volume, error) {
	if items, ok := jsonList[domain.Volume](stdout, "volumes", "volume"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "volume list", volumeColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Volume, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Volume{
			Name:       row["name"],
			Driver:     row["driver"],
			Mountpoint: row["mountpoint"],
			Scope:      row["scope"],
			CreatedAt:  row["created"],
			Size:       row["size"],
			Labels:     row["labels"],
		})
	}
	return out, nil
}

// ParseNetworks parses `wslc network list` output (JSON or table).
func ParseNetworks(stdout string) ([]domain.Network, error) {
	if items, ok := jsonList[domain.Network](stdout, "networks", "network"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "network list", networkColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Network, 0, len(rows))
	for _, row := range rows {
		n := domain.Network{
			ID:        row["id"],
			Name:      row["name"],
			Driver:    row["driver"],
			Scope:     row["scope"],
			IPv6:      row["ipv6"],
			Internal:  row["internal"],
			CreatedAt: row["created"],
			Labels:    row["labels"],
		}
		n.Containers = splitList(row["containers"])
		out = append(out, n)
	}
	return out, nil
}

// ParseStats parses `wslc stats` output (JSON or table).
func ParseStats(stdout string) ([]domain.ContainerStats, error) {
	if items, ok := jsonList[domain.ContainerStats](stdout, "stats", "containers"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "stats", statsColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ContainerStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ContainerStats{
			ID:       row["id"],
			Name:     row["name"],
			CPUPerc:  row["cpuperc"],
			MemUsage: row["memusage"],
			MemPerc:  row["memperc"],
			NetIO:    row["netio"],
			BlockIO:  row["blockio"],
			PIDs:     domain.Int64(parseInt(row["pids"])),
		})
	}
	return out, nil
}

// ParseSessions parses `wslc system session list` output.
//
// That command rejects --format, so the table path is the real one; JSON is
// still accepted because `wslc info --format json` nests the same objects.
func ParseSessions(stdout string) ([]domain.Session, error) {
	if items, ok := jsonList[domain.Session](stdout, "sessions", "session"); ok {
		return nonNil(items), nil
	}
	rows, err := parseTable(stdout, "session list", sessionColumns)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Session{
			ID:         parseInt(row["id"]),
			Name:       row["name"],
			CreatorPid: parseInt(row["creatorpid"]),
		})
	}
	return out, nil
}

// ParseSystemInfo parses `wslc info` output, both --format json and the default
// key/value table with the trailing session table.
func ParseSystemInfo(stdout string) (domain.SystemInfo, error) {
	var info domain.SystemInfo
	if isBlank(stdout) {
		return info, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(stdout, "\ufeff"))
	if raw != "" && (raw[0] == '{' || raw[0] == '[') {
		if decodeSystemInfoJSON(raw, &info) {
			return info, nil
		}
	}

	lines := tableLines(stdout)
	recognized := false
	for i := 0; i < len(lines); i++ {
		if key, value, ok := splitKeyValue(lines[i]); ok {
			if field, known := infoLabels[normalizeColumn(key)]; known {
				applyInfoField(&info, field, value)
				recognized = true
			}
			continue
		}
		fields, starts, ok := sessionTableHeader(lines[i])
		if !ok {
			continue
		}
		info.Server.Sessions = parseSessionRows(fields, starts, lines[i+1:])
		recognized = true
		break
	}
	if !recognized {
		return info, unrecognizedOutput("info", firstLines(stdout, 2))
	}
	if info.Server.Sessions == nil {
		info.Server.Sessions = []domain.Session{}
	}
	return info, nil
}

// decodeSystemInfoJSON decodes the Client/Server object toleratedly: unknown
// keys are ignored, missing keys stay zero and a malformed section does not
// fail the whole parse.
func decodeSystemInfoJSON(raw string, info *domain.SystemInfo) bool {
	var sections map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		return false
	}
	for key, value := range sections {
		switch strings.ToLower(key) {
		case "client":
			var client domain.ClientInfo
			if err := json.Unmarshal(value, &client); err == nil {
				info.Client = client
			}
		case "server":
			var server domain.ServerInfo
			if err := json.Unmarshal(value, &server); err == nil {
				info.Server = server
			}
		}
	}
	return true
}

func applyInfoField(info *domain.SystemInfo, field, value string) {
	switch field {
	case "version":
		info.Client.Version = value
	case "kernel":
		info.Client.KernelVersion = value
	case "direct3d":
		info.Client.Direct3DVersion = value
	case "dxcore":
		info.Client.DxCoreVersion = value
	case "windows":
		info.Client.WindowsVersion = value
	case "settingsfile":
		info.Client.SettingsFile = value
	case "sessionmanager":
		info.Server.SessionManagerVersion = value
	}
}

// sessionTableHeader recognizes a line that is the header of the session
// sub-table embedded in `wslc info`.
func sessionTableHeader(line string) ([]string, []int, bool) {
	cells, starts := splitColumnsAt(line)
	if len(cells) < 2 {
		return nil, nil, false
	}
	fields := make([]string, len(cells))
	recognized := 0
	for i, cell := range cells {
		if field, ok := sessionColumns[normalizeColumn(cell)]; ok && field != "" {
			fields[i] = field
			recognized++
		}
	}
	if recognized < 2 {
		return nil, nil, false
	}
	return fields, starts, true
}

// parseSessionRows parses the rows that follow a session table header.
func parseSessionRows(fields []string, headerStarts []int, lines []string) []domain.Session {
	var out []domain.Session
	for _, line := range lines {
		if isNoiseLine(line) {
			continue
		}
		if _, _, ok := splitKeyValue(line); ok {
			// A new key/value section started; the session table is over.
			break
		}
		cells, starts := splitColumnsAt(line)
		if len(cells) == 0 {
			continue
		}
		aligned := alignRow(line, cells, starts, headerStarts, len(fields))
		var session domain.Session
		for i, field := range fields {
			switch field {
			case "id":
				session.ID = parseInt(aligned[i])
			case "creatorpid":
				session.CreatorPid = parseInt(aligned[i])
			case "name":
				session.Name = aligned[i]
			}
		}
		out = append(out, session)
	}
	if out == nil {
		out = []domain.Session{}
	}
	return out
}

// splitKeyValue splits an `info` line of the form "label: value".
func splitKeyValue(line string) (string, string, bool) {
	idx := strings.IndexAny(line, ":：")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	if key == "" || strings.Contains(key, "  ") {
		return "", "", false
	}
	return key, value, true
}

// jsonList reports whether stdout is JSON and, if so, decodes it as a list.
//
// Tolerated shapes:
//
//   - an array: [{...},{...}]
//   - a single object: {...}
//   - an object wrapping the array under one of wrapperKeys: {"Volumes":[{...}]}
//   - NDJSON, i.e. one object per line, which is what wslc 3.0.1 actually
//     prints for `volume list --format json` and `network list --format json`:
//     {"Name":"a",...}\n{"Name":"b",...}
//
// Anything that is not JSON at all returns ok=false so the caller falls back to
// the table parser.
func jsonList[T any](stdout string, wrapperKeys ...string) ([]T, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(stdout, "\ufeff"))
	if raw == "" {
		return nil, false
	}
	switch raw[0] {
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &elements); err != nil {
			return nil, false
		}
		return decodeElements[T](elements), true
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &object); err != nil {
			// The input is more than one JSON value, so it cannot be a single
			// object: this is the newline-delimited shape listed above.
			return decodeNDJSON[T](raw)
		}
		for key, value := range object {
			for _, want := range wrapperKeys {
				if !strings.EqualFold(key, want) {
					continue
				}
				var elements []json.RawMessage
				if err := json.Unmarshal(value, &elements); err != nil {
					var single T
					if err := json.Unmarshal(value, &single); err != nil {
						return nil, false
					}
					return []T{single}, true
				}
				return decodeElements[T](elements), true
			}
		}
		if len(object) == 0 {
			return []T{}, true
		}
		var single T
		if err := json.Unmarshal([]byte(raw), &single); err != nil {
			return nil, false
		}
		if reflect.DeepEqual(single, *new(T)) {
			// An object that carries nothing this type understands (for example
			// `wslc info --format json` fed to ParseContainers) is not a list.
			return nil, false
		}
		return []T{single}, true
	}
	return nil, false
}

// decodeNDJSON walks an NDJSON stream (one JSON value per line) and returns
// every value that decodes into T. wslc emits it for `volume list`,
// `network list` and `image list`: it prints one object (and `network list
// --format json` one object per network) followed by a newline.
//
// The stream is walked with a json.Decoder, which copes with CRLF, trailing
// whitespace and an object that spans several lines. A value that cannot be
// decoded is isolated at the next boundary (newline, or the next `{`/`[` if
// there is none) and the walk resumes there, so one malformed chunk cannot
// silently lose every subsequent line: a bad line followed by a good object
// still yields that good object.
//
// ok is false only when nothing decodable was found, which lets the caller fall
// back to the table parser for non-JSON output.
func decodeNDJSON[T any](raw string) ([]T, bool) {
	remaining := raw
	out := make([]T, 0, 8)
	sawValue := false
	for len(remaining) > 0 {
		dec := json.NewDecoder(strings.NewReader(remaining))
		var element json.RawMessage
		err := dec.Decode(&element)
		if err == nil {
			sawValue = true
			var value T
			if err := json.Unmarshal(element, &value); err == nil {
				out = append(out, value)
			}
			// Continue right after the value that was just decoded, so several
			// values on one line keep working and pretty-printed objects are
			// not cut apart.
			consumed := dec.InputOffset()
			if consumed <= 0 || consumed > int64(len(remaining)) {
				break
			}
			remaining = remaining[consumed:]
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		// Malformed input. Prefer the next newline; when there is none, start
		// over at the next object/array opener so a trailing run of bad bytes
		// does not discard every well-formed value after it.
		cut := -1
		if i := strings.IndexByte(remaining, '\n'); i >= 0 {
			cut = i + 1
		} else if i := strings.IndexAny(remaining, "{["); i >= 0 {
			cut = i
		}
		if cut <= 0 || cut >= len(remaining) {
			break
		}
		remaining = remaining[cut:]
	}
	if !sawValue {
		// Not JSON at all: the caller must fall back to the table parser.
		return nil, false
	}
	return out, true
}

// decodeElements decodes every element that fits T, skipping the ones that do
// not, so one unexpected element cannot lose the whole list.
func decodeElements[T any](elements []json.RawMessage) []T {
	out := make([]T, 0, len(elements))
	for _, element := range elements {
		var value T
		if err := json.Unmarshal(element, &value); err != nil {
			continue
		}
		out = append(out, value)
	}
	return out
}

// nonNil turns a nil slice into an empty one so JSON encodes it as [].
func nonNil[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

// splitList splits a comma-separated table cell (names, ports, …) into a
// StringList, dropping empty entries.
func splitList(value string) domain.StringList {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make(domain.StringList, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, `"`))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseInt parses a table cell that holds an integer, tolerating decorations.
func parseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	digits := strings.TrimFunc(value, func(r rune) bool { return r < '0' || r > '9' })
	if digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}

// isAbsoluteTime reports whether a CREATED value is an absolute timestamp
// rather than a relative "2 hours ago" style value.
func isAbsoluteTime(value string) bool {
	return absoluteTime.MatchString(strings.TrimSpace(value))
}
