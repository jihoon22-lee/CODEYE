# W09 구조화 보고서 게시와 내보내기

제품 워커가 수집한 Go vet 결과를 관리 서버에서 검증해 고정된 보고서 revision으로 게시한다. [실제 반환 예시](../examples/report-go-vet.json)는 [Go 합성 입력](../fixtures/go-vet/example.go)을 분석한 기본 JSON 내보내기다. 원시 관찰 1건과 검토 전 후보 1건이 같은 진단을 가리키며, 확인된 조치 대상은 0건이다. 이 실행의 AI 검토는 `not_performed`, 보고서 상태는 `partial`, 판정은 `inconclusive`다.

## 게시 시점과 입력

1. 노드가 스냅샷을 고정하고 원시 stdout/stderr, 관찰, 결과 manifest를 업로드한다. 모든 task가 실패해도 실행한 task가 있으면 결과 manifest 업로드를 시도한다.
2. 관리 서버가 release와 cleanup 확인을 마친 현재 attempt를 선택한다. PostgreSQL task 행에서 성공 여부를 읽고, 스냅샷 manifest와 디코딩할 결과 문서의 실제 크기와 SHA-256을 다시 확인한다.
3. 결과 manifest의 run·attempt·epoch·스냅샷·task 식별자와 산출물 참조를 저장된 행에 대조한다. Go vet 원시 참조는 해당 task의 `go.vet` stdout ID, SHA-256, 바이트 범위를 가리킨다. 스냅샷 manifest에서 경로와 blob hash가 일치해야 도구 위치를 finding에 연결한다. 도구가 확정한 줄 범위를 사용한다.
4. 같은 트랜잭션에서 현재 epoch·release·cleanup·manifest·run 상태를 다시 확인한 뒤 보고서 본문, run 종결 상태, 게시 이벤트를 저장한다. 서버 재시작 시 대기 중인 보고서를 처리한다. 동시 게시나 재시도에는 입력 digest와 revision의 유일 제약을 적용한다.
5. 스냅샷 고정 뒤 실패·취소된 실행은 확인할 수 있는 사실을 담은 해당 상태의 보고서를 게시할 수 있다. 스냅샷 고정 전 종료된 실행은 run 상태와 사유로 추적한다. 저장 오류로 도중에 멈춘 경우 다음 reconcile에서 재시도한다. 검증할 수 없는 입력의 영구 거부는 한 번 기록한다.

## 판정과 내보내기

- Go vet 진단에서 만든 후보는 `unreviewed`다. 원시 관찰·후보·조치 대상·반박된 항목의 집계와 우선순위는 구조화된 finding과 observation에서 계산한다. 누락·깨진 산출물, 미분석 파일, 스냅샷 불일치는 제한 사항에 남긴다.
- 세 프로필 모두 `review.context`, `review.investigate`, `review.verify`의 DB 성공과 등록 디코더의 결과 수락이 필요하다. W09 기본 디코더는 Go vet 관찰을 처리한다. 검토 기록이 없거나 필수 도구 결과가 누락되면 `no_actionable_findings`를 게시할 수 없다.
- `GET /api/v1/runs/{id}/report`는 최신 revision을 반환한다. `revision=N`, `format=json|markdown|html`, `include_code=true|false`를 지원한다. 기본 내보내기는 원시 도구 메시지·코드 식별자·자유 서술을 생략한다. `include_code=true`에서 저장된 도구 메시지와 근거를 제공한다. 관리 서버에는 snapshot 파일 본문이 없어 코드 발췌를 노드 원본 경로에서 다시 읽지 않는다.
- `GET /api/v1/runs/{id}/reports`로 revision·저장 본문 해시·크기와 지원 형식을 조회한다. 권한은 요청마다 확인한다. 프로젝트 멤버가 아닌 사용자의 두 경로는 404를 반환한다.
- 세 형식은 같은 JSON projection, 집계와 revision을 사용한다. `X-CODEYE-Report-SHA256`은 해당 projection의 JSON 해시, `ETag`는 실제 응답 바이트의 해시다. Markdown과 HTML은 첨부 파일로 제공한다. HTML은 문맥별 escaping과 외부 로딩을 막는 CSP를 사용한다.

## 검증과 제한

| 수용 시험 | 실제 확인 항목 |
|---|---|
| TC-80 | W08 관찰·해시·task별 stdout·스냅샷 경로 바인딩, 누락된 실행 행, 출처가 다른 위치 격리 |
| TC-81 | AI task가 성공해도 미수락 결과는 미수행, 필수 도구 결과 누락과 미해결 관찰의 clean 판정 차단 |
| TC-82 | 동일 입력·시각·보고서 ID에서 재생성한 JSON 동일, Markdown·HTML과 집계·식별자·projection 해시 일치 |
| TC-83 | 실제 PostgreSQL에서 동시 게시·stale epoch·취소 경쟁·실패·영구 거부·서버 재시작, 실제 제품 워커의 Go vet 보고서 |
| TC-84 | 멤버와 비멤버·권한 회수, 코드 제외/포함, 위험한 도구 문자열 escaping, 형식별 응답 헤더와 revision 선택 |

Linux에서 `python3 scripts/test_postgres.py --nodes`가 임시 PostgreSQL, 관리 서버, 여러 노드와 실제 워커를 사용한다. `go test -race ./...`의 report·HTTP·저장 테스트와 CLI 재적용도 이 명령에 포함된다. 독립된 `python3 scripts/validate_bundle.py`는 schema, 예시 및 잘못된 근거 참조를 검사한다. Windows·RHEL Native 실행과 브라우저의 보고서 상세 화면은 이 단계의 시험 범위 밖이다. 모델 품질, 검토자의 disposition, 내보내기 감사 이벤트는 이 결과로 측정하지 않았다.
