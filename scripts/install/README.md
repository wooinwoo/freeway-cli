# 공급처 설치·업데이트

체크아웃한 Freeway 소스에서 사용하는 Node와 공식 OpenCode·Cline CLI를 준비합니다. Go 서버·TUI 바이너리 빌드는 별도 제품 배포 영역입니다.

| 대상 | 설치·연결 방법 |
| --- | --- |
| 공통 | Freeway 전용 Node.js·npm |
| Kilo, NVIDIA, Z.ai, Requesty, Vikasit, OpenRouter, Groq, Gemini API, Novita | HTTP API. 업체 프로그램 설치 없이 키 구성. Kilo 무료 경로는 익명 연결 가능 |
| OpenCode | 공식 opencode-ai 패키지·OS별 네이티브 실행 파일 |
| Cline | 공식 cline 패키지·OS별 네이티브 실행 파일 |

`openai_compatible`은 HTTP 형식입니다. OpenAI 소속·계정·CLI를 뜻하지 않습니다. 공급처 목록의 `transport`, `protocol`, `requiresCLI`로 구분하며 예전 `kind: openai`는 호환용입니다.

권장 버전의 단일 기준은 [versions.json](versions.json)입니다. 현재 Node 26.13.1, OpenCode 1.18.35, Cline 3.0.62입니다. Node는 [공식 서버](https://nodejs.org/dist/)에서 받고 릴리스 SHA256을 검증합니다. npm은 공식 레지스트리의 고정 버전·무결성 검사를 사용합니다. OpenCode 최신 V2와 현재 브리지의 서버 API가 다르므로 임의 latest로 올리지 않습니다.

## 설치

저장소 루트에서 실행합니다. Node가 없는 PC도 bootstrap을 사용합니다. 기본 위치는 사용자 홈의 `.freeway/providers`이며 관리자 권한·전역 npm 설치·시스템 PATH 변경은 필요하지 않습니다.

Linux / WSL / macOS Bash:

```sh
bash scripts/install/providers.sh install
bash scripts/install/providers.sh install --api-only
bash scripts/install/providers.sh install --only opencode
```

Windows PowerShell:

```powershell
powershell -NoProfile -File scripts/install/providers.ps1 -Action install
powershell -NoProfile -File scripts/install/providers.ps1 -Action install -ApiOnly
powershell -NoProfile -File scripts/install/providers.ps1 -Action install -Only opencode
```

기본 설치는 두 CLI를 준비합니다. API-only는 Node만 설치합니다. `--only cline` 또는 `-Only cline`도 가능하며 API-only와 Only는 함께 사용하지 않습니다.

설치 없이 도움말을 보려면 Bash `--help`, PowerShell `-Help`를 사용합니다.

다른 전용 폴더는 Bash `--root /absolute/path`, PowerShell `-Root C:\absolute\path`로 지정합니다. 루트·사용자 홈 자체·저장소 루트는 설치 대상으로 허용하지 않습니다. Bootstrap 대상은 glibc Linux·macOS의 x64/arm64, Windows x64/arm64입니다.

## 버전 확인·업데이트

```sh
bash scripts/install/providers.sh check
bash scripts/install/providers.sh update
```

```powershell
powershell -NoProfile -File scripts/install/providers.ps1 -Action check
powershell -NoProfile -File scripts/install/providers.ps1 -Action update
```

`check`는 설치·인증 없이 실제 버전의 `installedVersion`, `recommendedVersion`, `updateAvailable`, `status`를 반환합니다. Node가 없거나 새 권장 런타임이 필요하면 bootstrap이 `runtime_not_installed_or_update_required`를 반환할 수 있습니다.

`update`는 현재 소스의 versions.json에 지정한 검증 버전으로 맞춥니다. 저장소를 git pull하거나 npm latest를 자동 설치하지 않습니다. Freeway 소스·배포본 갱신 후 update를 실행하면 새 검증 버전이 적용됩니다. 같은 버전은 재다운로드하지 않습니다. API-only·단일 CLI 프로필은 이후 기본 check·update에도 유지됩니다.

새 폴더의 버전을 검증한 뒤 활성 install.json을 원자적으로 전환하고 이전 파일은 유지합니다. 실패 시 기존 manifest를 보존합니다. 경로가 바뀌면 `restartRequired: true`이며, 실행 중 게이트웨이는 대화를 마친 뒤 정상 종료·시작합니다. 설치기가 다른 앱의 CLI나 진행 중 대화를 강제 종료하지 않습니다.

## 설치 후 인증·사용

```sh
"$HOME/.freeway/providers/bin/freeway-providers" auth all
"$HOME/.freeway/providers/bin/freeway-providers" status all
"$HOME/.freeway/providers/bin/freeway-providers" models kilo
"$HOME/.freeway/providers/bin/freeway-providers" quota all
"$HOME/.freeway/providers/bin/freeway-providers" ask kilo --model reflection/beam-free --reasoning-effort high -- "인사해 주세요."
"$HOME/.freeway/providers/bin/freeway-provider-setup" update
```

Windows:

```powershell
& "$HOME/.freeway/providers/bin/freeway-providers.ps1" auth all
& "$HOME/.freeway/providers/bin/freeway-providers.ps1" models kilo
& "$HOME/.freeway/providers/bin/freeway-providers.ps1" quota all
& "$HOME/.freeway/providers/bin/freeway-providers.ps1" ask kilo --model reflection/beam-free --reasoning-effort high "--" "인사해 주세요."
& "$HOME/.freeway/providers/bin/freeway-provider-setup.ps1" -Action update
```

PowerShell 5.1의 질문 구분자는 `"--"`로 인용합니다. 예: `ask kilo "--" "--model이라는 문자열을 설명해 주세요."`. 인용하지 않은 구분자는 PowerShell 자체 처리에서 사라질 수 있습니다. Go API는 셸 대신 argv·stdin JSON을 직접 사용합니다.

`authenticationConfigured: false`는 설치기가 인증을 수행하지 않았다는 뜻이며 기존 로그인 유무 판정이 아닙니다. `auth all`이 키 숨김 입력·기존 로그인 재사용을 처리하고 필요한 기기·브라우저 인증은 공식 화면에서 완료합니다. 추론 성공은 probe나 실제 호출로 별도 확인합니다.

## 백엔드 발견 경로

install.json에는 실행 파일 경로·버전만 저장합니다. Go providers.New는 NodePath를 생략하면 이 파일의 Node를 먼저 쓰고 없으면 PATH에서 찾습니다. Node 브리지는 명시한 OPENCODE_BIN·CLINE_BIN, 전용 설치, PATH 순으로 CLI를 찾습니다. custom root라면 앱·게이트웨이에도 FREEWAY_PROVIDER_HOME을 설정합니다.

launchers는 소스의 절대 경로를 사용합니다. 소스 폴더를 옮겼다면 새 위치에서 설치 명령을 다시 실행합니다. [공급처 README](../../backend/internal/providers/README.md)·[API 명세](../../backend/internal/providers/API.md)를 함께 사용합니다.

## 실패·취소

- 다운로드·SHA256·버전 검사 실패를 성공으로 표시하지 않습니다.
- npm은 전용 prefix와 ignore-scripts를 사용합니다. 기존 Cline hub를 바꿀 수 있는 postinstall도 실행하지 않고 공식 네이티브 패키지를 직접 찾습니다.
- 중복 설치는 락으로 거절합니다. 정상 취소는 자식 종료 후 락을 해제합니다. 죽은 PID의 락은 소유자 확인 후 회수합니다.
- 소유자 불명·알 수 없는 파일이 있는 불완전 락은 자동 삭제하지 않습니다. 프로세스 종료와 전용 폴더를 점검해야 합니다.
- 사용자 로그인 저장소·기존 게이트웨이 데이터는 설치 폴더와 별도입니다.

검증: `node --test tests/install/providers.test.mjs`. Windows 실제 설치·인증과 macOS 실행은 별도 플랫폼 검증이 필요합니다.

## 코드 구조

| 파일 | 책임 |
| --- | --- |
| `providers.sh` · `providers.ps1` | OS별 Node 다운로드·검증·bootstrap |
| `providers.mjs` | 설치·업데이트·버전 확인과 manifest 전환 |
| `lib/process.mjs` | 자식 프로세스 실행·시간 제한·취소 |
| `lib/lock.mjs` | 중복 설치 방지·종료된 설치의 안전한 락 회수 |
| `lib/launchers.mjs` | 설치 위치에 맞는 실행 명령 생성 |
| `versions.json` | 검증한 권장 버전의 단일 기준 |
