# 공급처 도구·공식 CLI 설치

Freeway 전체 설치는 루트의 [install.sh](../../install.sh)·[install.ps1](../../install.ps1)을 사용합니다. 현재 소스 설치 명령은 `bash install.sh --source`, Windows는 `powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Source`입니다. 배포 패키지는 [네이티브 패키징 안내](../package/README.md)를 참고하세요. 아래 내용은 공급처 도구만 별도로 준비하거나 공식 공급처 CLI를 설치하는 경로입니다.

Go 기반 공급처 도구와 공식 OpenCode·Cline 네이티브 실행 파일을 준비합니다. 백엔드 서버·TUI 설치 작업은 포함하지 않습니다. Node·npm·POC 게이트웨이는 실행하지 않습니다.

| 대상 | 설치 |
| --- | --- |
| freeway-providers | 이 체크아웃의 cmd/providerctl을 Go로 빌드 |
| HTTP API 9곳 | 업체 프로그램 설치 없음. 키·필요한 무료 계정 확인만 구성 |
| OpenCode·Cline | 공식 registry의 고정 네이티브 패키지를 Go가 직접 내려받음 |

## 준비·설치

소스에서 빌드하려면 [Go 1.26 이상](https://go.dev/doc/install/)이 필요합니다. go version으로 확인합니다. 기본 설치 위치는 사용자 홈의 .freeway/providers이며 전역 PATH·npm·기존 CLI는 변경하지 않습니다.

```sh
bash scripts/install/providers.sh install
bash scripts/install/providers.sh install --api-only
bash scripts/install/providers.sh install --only opencode
```

Windows:

```powershell
powershell -NoProfile -File scripts/install/providers.ps1 -Action install
powershell -NoProfile -File scripts/install/providers.ps1 -Action install -ApiOnly
powershell -NoProfile -File scripts/install/providers.ps1 -Action install -Only cline
```

도움말은 --help 또는 -Help입니다. API-only는 Go 공급처 도구만 준비합니다. --only cline도 가능합니다. API-only와 Only는 동시에 사용하지 않습니다.

사용자 지정 폴더는 Bash --root 또는 PowerShell -Root로 지정합니다. 이후 같은 폴더를 쓰려면 FREEWAY_PROVIDER_HOME에도 같은 절대 경로를 지정합니다.

신뢰한 소스가 PowerShell 실행 정책에 걸리는 경우 현재 프로세스에만 -ExecutionPolicy Bypass를 적용할 수 있습니다. 시스템·조직 정책은 변경하지 않습니다. [공식 설명](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies?view=powershell-5.1)을 따릅니다.

## 버전·업데이트

```sh
"$HOME/.freeway/providers/bin/freeway-providers" check
"$HOME/.freeway/providers/bin/freeway-providers" update
```

검증 버전의 단일 기준은 [Go versions.json](../../backend/internal/providers/versions.json)입니다. 임의 latest로 업데이트하지 않습니다. Freeway 소스를 갱신한 뒤 bootstrap을 다시 실행하면 도구 자체를 다시 빌드합니다.

설치기는 공식 패키지 메타데이터의 SHA512 integrity를 검증하고, 안전한 전용 폴더에 압축을 풀어 실행 버전을 확인한 다음 활성 install.json을 전환합니다. 이전 버전 파일은 유지합니다. 중복 설치는 OS 잠금으로 거절하고 실패·취소를 성공으로 표시하지 않습니다. 실행 중인 다른 앱의 CLI는 종료하지 않습니다.

check는 다운로드·인증을 하지 않고 실제 설치 버전을 반환합니다. 설치 전 bootstrap check는 도구가 없다는 결과를 반환하며 컴파일러·파일 생성도 요구하지 않습니다. API-only·단일 CLI 프로필은 이후 기본 check·update에 유지됩니다.

## 인증·실제 응답

```sh
provider_cli="$HOME/.freeway/providers/bin/freeway-providers"
"$provider_cli" auth kilo
"$provider_cli" auth cline --login
"$provider_cli" models kilo
"$provider_cli" probe kilo --model reflection/beam-free --reasoning-effort high
"$provider_cli" quota all
```

Windows는 freeway-providers.exe 또는 생성된 .ps1을 사용합니다. API 키는 숨김 입력·환경변수·stdin으로 받고 auth 하위 폴더에 암호화합니다. 공식 CLI 로그인은 해당 CLI가 관리합니다. 설치 성공·인증 구성과 실제 추론 성공은 별도로 확인합니다.

[설치부터 호출까지 예제](../../backend/internal/providers/examples/README.md) · [Go API 명세](../../backend/internal/providers/API.md)

## 구현·검증

설치 로직은 [install.go](../../backend/internal/providers/install.go), 인증·모델 도구는 [providerctl](../../backend/cmd/providerctl/main.go)에 있습니다. Bash·PowerShell은 Go 소스 빌드와 인수 전달만 담당합니다.

```sh
go -C backend test -race ./...
```

공급처 키가 없는 API 8곳의 실호출·새 계정 브라우저 인증은 별도 검증 대상입니다. 설치 폴더의 예전 Node 파일과 기존 POC 사용자 데이터는 자동 삭제하지 않습니다.
