# CODEYE

CODEYE는 프로젝트의 코드를 고정된 스냅샷에서 정적 분석하고, 모델이 제시한 후보를 코드 근거와 반례로 검토하는 자체 호스팅 QA 플랫폼입니다. 보고서에서 발생 조건, 예상 영향, 근거 위치와 미검토 범위를 확인할 수 있습니다.

제품 범위는 프로젝트 소스를 직접 확보해 검사하는 Native 실행입니다. 예약·다언어 분석 팩·GHES App 연동의 단계별 계획은 [제품 범위](docs/00-product-and-scope.md)에 있습니다.

## 지금 사용할 수 있는 기능

개인 모드에서 프로젝트를 등록하고 분석을 요청하면 PostgreSQL에 실행이 기록됩니다. 등록한 Native 노드가 작업을 받아 별도 사본에서 Go vet를 실행합니다. 모델 전송을 승인한 로컬 소스에서는 OpenCode를 통해 맥락 파악, 독립 코드 조사, 후보 검증을 수행합니다. 관리 서버가 결과를 게시하면 웹에서 보고서를 읽고 같은 리비전을 JSON·Markdown·HTML로 내려받을 수 있습니다. [개인 모드 실행](docs/24-w03-api.md) · [노드 등록](docs/28-w06-node-protocol.md) · [에이전트 실행](docs/28-w06-agent-runtime.md) · [보고서 읽기](docs/35-w13-evidence-report.md)

R0 파일럿은 한 WSL2 호스트에서 두 노드, 합성 전후 10쌍, 공개 CODEYE 사본과 별도 Git 입력으로 검증했습니다. Git 입력은 SHA를 고정하고 모델 전송 정책에 따라 부분 완료로 게시했습니다. Windows CI는 Go 시험을 수행했습니다. RHEL 8.10 운영과 다중 물리 노드는 후속 검증 대상입니다. [파일럿 결과](docs/36-r0-pilot.md)

## 개발과 검증

저장소 루트에서 Go 바이너리와 정적 웹을 빌드합니다. R0 개발 환경은 Go 1.27.4와 Node 24.18.0입니다. DB 접속과 일회용 로그인 링크 설정은 [개인 모드 절차](docs/24-w03-api.md)에 있습니다.

```bash
go build -o bin/codeye ./cmd/codeye
npm --prefix web ci
npm --prefix web run build
./bin/codeye doctor --format json
```

`doctor`의 종료 코드 1은 미설정·미검증·사용 불가 항목이 있다는 뜻입니다. 모델 요청과 워커 시험은 별도로 확인합니다. [진단 계약](docs/21-w01-foundation.md) · [모델 연결 시험](docs/32-w10-opencode.md)

문서와 예시 검증은 `python3 scripts/validate_bundle.py`, 웹 시험은 `npm --prefix web test`입니다. Native PostgreSQL 도구·Chromium·Python 시험 의존성이 준비된 환경에서는 `python3 scripts/test_postgres.py --web --nodes --scheduler`로 제품 경로를 검증합니다. [시험 환경과 재실행 방법](docs/36-r0-pilot.md)

## 다음 개발 단계

R1(W15~W25)을 마쳤습니다. 모듈별 환경·문서 탐색과 단계별 진행 표시, 다섯 언어 분석 팩, 실행 비교와 사람 판정, 개인 모드 운영(start·stop·백업·복원)을 쓸 수 있습니다. 기본 검토 지침은 [`review-r1` 2.0.0](docs/49-review-pack-default.md)입니다. 다음 범위는 R2(W26~)이며 착수는 소유자가 정합니다. [개인 사용 안내서](docs/48-user-guide.md) [작업 순서](docs/17-implementation-plan.md) · [역할별 지침](docs/07-llm-and-instructions.md) · [화면 예시](docs/12-report-and-ui.md)

[문서 인덱스](docs/INDEX.md)에서 제품 범위, 계약, 실행 근거를 찾을 수 있습니다. 저장소 개발 절차는 [AGENTS.md](AGENTS.md)에 있습니다. 라이선스는 소유자가 결정합니다.
