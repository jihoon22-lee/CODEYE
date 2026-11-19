# CODEYE {{version}} 설치 ({{os}}/{{arch}}, 인터넷 없는 머신)

이 묶음은 `codeye` 실행 파일 하나(정적 링크, CGO 없음), 빌드된 웹 화면(`web/dist`), 문서(`docs/GUIDE.md`, `docs/OPERATIONS.md`), 오프라인 설정 예시(`examples/personal-offline.json`), `MANIFEST.json`, `SHA256SUMS`를 담는다. DB migration과 검토 지침 팩은 실행 파일 안에 들어 있다. 분석 도구, 언어 toolchain, PostgreSQL, OpenCode, 프로젝트 의존성은 들어 있지 않다(`docs/OPERATIONS.md`의 "폐쇄망 배포팩").

## 1. 확인

옮겨 온 묶음이 만든 것과 같은지 확인한다.

```sh
sha256sum -c {{top}}.tar.gz.sha256      # 묶음 파일 자체
tar -xzf {{top}}.tar.gz
cd {{top}}
sha256sum -c SHA256SUMS                  # 풀어 놓은 파일 전부
./codeye version                         # MANIFEST.json의 version·commit과 같아야 한다
```

## 2. 놓을 곳

풀어 놓은 디렉터리를 그대로 원하는 곳(예: `~/.local/opt/{{top}}`)에 둔다. `codeye`는 옆의 `web/dist`를 웹 화면으로 쓴다. 실행 파일만 옮기면 설정에 `web_dir`을 적는다.

## 3. 설정

`examples/personal-offline.json`을 `~/.config/codeye/personal.json`으로 복사하고 주소와 경로를 이 머신의 것으로 바꾼다. 쓰지 않는 미러 항목은 지운다. 미러가 하나도 없으면 `"offline": true`만 있어도 된다. 그때 Go 의존성은 `agent.go_mod_cache`(미리 받아 둔 모듈 캐시)에서만 읽고, 나머지는 받지 않고 `offline_no_registry_configured`로 보고서에 남는다.

모델 검토를 쓰려면 사내 OpenAI 호환 주소를 담은 키 파일을 `<state_dir>/secrets/model.json`(권한 0600)에 둔다.

```json
{"schema_version": "0.1", "provider": "openai", "api_key": "...", "base_url": "https://llm.corp.example/v1"}
```

사내 CA가 있으면 시스템 CA와 사내 CA를 합친 PEM 파일을 `ca_bundle`에 적는다. 인증서 검증을 끄지 않는다.

## 4. 점검과 실행

```sh
./codeye tools --checklist               # 분석 도구 점검표: 없는 도구와 옮길 위치
./codeye doctor --config ~/.config/codeye/personal.json   # "Offline readiness": 나가는 주소, CA, PostgreSQL, 언어별 빠진 것
./codeye up --offline ~/src/my-project   # 처음 한 번 --offline을 주면 설정에 저장된다
```

PostgreSQL 프로그램이 PATH에 없으면 `--postgres-bin-dir DIR`을 준다. 자세한 절차는 `docs/GUIDE.md`, 오프라인 운영과 미러 설정은 `docs/OPERATIONS.md`에 있다.
