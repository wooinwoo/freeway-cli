# 네이티브 제품 패키징

Freeway 전체 설치의 진입점은 루트의 [install.sh](../../install.sh)·[install.ps1](../../install.ps1)입니다. 소스 설치는 `bash install.sh --source` 또는 `powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Source`로 실행합니다. 아래는 배포 아카이브를 직접 빌드하는 절차입니다.

Go 1.26 이상으로 저장소 루트에서 실행합니다. 패키지는 Go 실행 파일로 동작합니다.

```sh
go -C cli run ./cmd/package --root "$PWD"
```

기준 버전은 루트 `VERSION`입니다. Linux·macOS·Windows의 amd64·arm64를 빌드하고 `dist/native`에 tar.gz/ZIP, `release.json`, `SHA256SUMS`를 만듭니다. `--targets linux/amd64`, `--out PATH`, `--go PATH`, `--base-url HTTPS_URL`로 빌드·출력·배포 주소를 지정합니다. 파일을 업로드하거나 릴리스를 공개하지 않습니다.

패키지는 `freeway`, `freeway-providers`, `freewayd`, `manifest.json`, 프로젝트·Go·실제 빌드 의존성의 라이선스와 고지를 포함합니다. 세 실행 파일은 같은 버전으로 빌드합니다. Go 캐시에 의존성 고지가 없으면 패키징이 실패합니다.

압축 파일을 처음 실행하기 전에 신뢰한 `SHA256SUMS`와 해시를 비교하세요. 압축을 푼 `freeway`로 실행합니다.

```sh
./freeway install --archive /absolute/path/freeway_VERSION_linux_amd64.tar.gz --sha256 EXPECTED_SHA256
~/.freeway/app/bin/freeway --version
```

기본 설치 경로는 `~/.freeway/app`입니다. 사용자 PATH에 그 `bin`을 추가합니다. Windows에서는 ZIP을 풀고 `freeway.exe`를 실행합니다. 관리자 권한은 필요하지 않습니다.

```sh
freeway update check
freeway update
freeway rollback
freeway uninstall
```

업데이트 출처가 아직 공개되지 않았다면 오류를 반환하고 기존 버전을 유지합니다. `--release-url`은 신뢰할 수 있는 사용자 지정 릴리스 명세용입니다. 계정과 대화는 프로그램 폴더 밖에 두며 제거 시 보존합니다.

자세한 사용자 안내는 [설치](../../docs/site/src/content/docs/ko/docs/installation.mdx)·[복구](../../docs/site/src/content/docs/ko/docs/updates.mdx), 공급처 공식 CLI 설치는 [별도 안내](../install/README.md)를 참고하세요.

검증:

```sh
FREEWAY_LIVE=0 go -C cli test -race ./...
python3 tests/e2e/native_smoke.py dist/native/freeway_0.6.0-dev_linux_amd64.tar.gz
```
