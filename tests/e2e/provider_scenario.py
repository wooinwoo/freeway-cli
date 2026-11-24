"""Exercise provider controls in the installed TUI without an external inference call."""
import json
import subprocess
import urllib.error
import urllib.request
from urllib.parse import parse_qs, urlsplit

from gateway_scenario import Terminal


def provider_smoke(program, environment, base):
    env = dict(
        environment, FREEWAY_GATEWAY_HOME=str(base / "control-gateway"),
        FREEWAY_PROVIDER_HOME=str(base / "control-providers"), FREEWAY_GATEWAY_PROVIDERS="gemini_api",
        GEMINI_API_KEY="fixture-environment-key",
    )

    def run(*args):
        return subprocess.check_output([str(program), *args], cwd=base, env=env, timeout=12, text=True)

    # Gemini API checks no key over the network, so the fixture key stays offline.
    run("providers", "auth", "gemini_api", "--from-env", "--confirm-free")
    credentials = base / "control-providers" / "auth" / "provider-auth.enc"
    before = credentials.read_bytes()
    info = json.loads(run("gateway", "connect"))

    def query(path):
        request = urllib.request.Request(info["url"] + path, headers={"Authorization": "Bearer " + info["apiKey"]})
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.load(response)["data"]

    assert [(status["id"], status.get("scopedCheck")) for status in query("/v1/providers")] == [("gemini_api", True)]
    assert [status["id"] for status in query("/v1/providers?provider=gemini_api")] == ["gemini_api"]
    try:
        query("/v1/providers?provider=disabled-fixture")
    except urllib.error.HTTPError as err:
        assert err.code == 404 and json.load(err)["error"]["code"] == "unknown_provider"
    else:
        raise AssertionError("A disabled provider was returned by the scoped status endpoint")

    # The UI may attach to the gateway from a shell with a different provider directory.
    ui_env = dict(env, FREEWAY_PROVIDER_HOME=str(base / "other-ui-providers"))
    with Terminal(program, [], ui_env, base) as terminal:
        terminal.wait_text("준비 완료")
        terminal.send(b"\x1bOS")
        terminal.wait_text("Enter 관리")
        terminal.send(b"\r")
        terminal.wait_text("Auto에서 제외")
        terminal.wait_text("API 키 교체")
        terminal.wait_text("공급처 잔여량 확인 불가")

        terminal.send(b"j\r")
        terminal.wait_text("등록된 키를 교체할까요")
        terminal.send(b"j\r")
        terminal.wait_text("무료 플랜을 확인했습니다")
        terminal.send(b"j\r")
        terminal.wait_text("API 키 입력")
        terminal.output.clear()
        terminal.paste("fixture-replacement-key", submit=True)
        terminal.wait_text("Enter 관리")
        assert b"fixture-replacement-key" not in terminal.output
        assert credentials.read_bytes() != before and not (base / "other-ui-providers" / "auth").exists()

        terminal.send(b"\r")
        terminal.wait_text("Auto에서 제외")
        terminal.output.clear()
        terminal.send(b"jj\r")
        terminal.wait_text("Auto 제외")
        models = query("/v1/models")
        assert models and "free/auto" not in [model["id"] for model in models]
        assert all(model["readyProviders"] == ["Gemini API"] for model in models)

        terminal.send(b"\r")
        terminal.wait_text("Auto에 포함")
        terminal.send(b"jjj\r")
        terminal.wait_text("Freeway에서 연결을 해제할까요")
        terminal.output.clear()
        terminal.send(b"\r")  # The default leaves the connection in place.
        terminal.wait_text("API 키 교체")
        assert query("/v1/providers")[0]["keyConfigured"]

        terminal.output.clear()
        terminal.send(b"\r")
        terminal.wait_text("Freeway에서 연결을 해제할까요")
        terminal.output.clear()
        terminal.send(b"j\r")
        terminal.wait_text("Freeway에서 연결 해제됨")
        status = query("/v1/providers")[0]
        assert status["state"] == "disconnected" and not status["keyConfigured"]
        assert query("/v1/models") == []

        terminal.output.clear()
        terminal.send(b"\r")
        terminal.wait_text("Freeway에서 다시 사용")
        terminal.output.clear()
        terminal.send(b"jj\r")
        terminal.wait_text("무료 플랜 확인 필요")
        status = query("/v1/providers")[0]
        assert status["state"] == "confirm_free_account" and status["autoExcluded"]
        terminal.output.clear()
        terminal.send(b"\x1b")
        terminal.wait_text("메시지")
        terminal.exit()
    print("F4 provider management: checked scoped status, replaced the right profile's key, checked usage, excluded Auto only and re-enabled safely.", flush=True)


def openrouter_login_smoke(program, environment, base):
    browser_dir = base / "oauth-browser"
    browser_dir.mkdir()
    opened = base / "openrouter-authorization"
    for browser in ("xdg-open", "open"):
        script = browser_dir / browser
        script.write_text("#!/bin/sh\n[ -z \"${FREEWAY_MCP_IMAGE_TOKEN+x}\" ] || exit 93\nprintf '%s' \"$1\" > \"$FREEWAY_TEST_OPENROUTER_URL\"\n")
        script.chmod(0o700)
    env = dict(
        environment, FREEWAY_GATEWAY_HOME=str(base / "oauth-gateway"),
        FREEWAY_PROVIDER_HOME=str(base / "oauth-providers"), FREEWAY_GATEWAY_PROVIDERS="openrouter",
        OPENROUTER_API_KEY="", PATH=str(browser_dir), WSL_DISTRO_NAME="", FREEWAY_TEST_OPENROUTER_URL=str(opened),
        FREEWAY_MCP_IMAGE_TOKEN="image-fixture-token",
    )
    with Terminal(program, ["connect", "openrouter"], env, base) as terminal:
        terminal.wait_text("브라우저로 로그인")
        terminal.send(b"\r")
        terminal.wait_text("OpenRouter에서 Freeway 승인")
        browser = urlsplit(opened.read_text())
        loaded = parse_qs(browser.query)
        callback = urlsplit(loaded["callback_url"][0])
        assert browser.scheme == "https" and browser.netloc == "openrouter.ai" and browser.path == "/auth"
        assert callback.scheme == "http" and callback.hostname == "localhost" and callback.port
        assert loaded["code_challenge_method"] == ["S256"] and loaded.get("state")
        assert "code_verifier" not in loaded
        terminal.send(b"\r")  # Switch to a separate, headless grant.
        terminal.wait_text("OpenRouter 인증 코드 입력")
        manual = parse_qs(urlsplit(opened.read_text()).query)
        assert "callback_url" not in manual and "state" not in manual
        assert manual["code_challenge"] != loaded["code_challenge"]
        terminal.send(b"\x1b")
        terminal.wait_text("OpenRouter 로그인을 취소했습니다")
        terminal.send(b"\x1b")
        assert terminal.child.wait(timeout=10) == 0
    assert not (base / "oauth-providers" / "auth" / "provider-auth.enc").exists()
    print("OpenRouter login: opened official PKCE URL, switched to a fresh headless grant, cancelled without saving a key.", flush=True)
