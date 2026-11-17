# W10 OpenCode 실연동

2026-11-18, Ubuntu 26.04.1 / WSL2 amd64, OpenCode 1.19.5, Go 1.27.4. REQ-026/030, TC-12/16/18/34의 관리형 로컬 CLI 범위를 검증했다. 모델을 지정하지 않은 설치에서는 요청을 보내지 않는다.

## 운영자가 지정하는 입력

`codeye model probe --executable ABSOLUTE_PATH --model opencode/MODEL --timeout 75s`는 합성 입력으로 실제 추론을 요청한다. 유료 시험에서는 `--model codeye-gateway/gpt-6.2-sol --relay-file ABSOLUTE_PATH`로 승인된 로컬 예산 relay를 선택한다. relay 구동과 누적 비용 장부는 [QA 설정](r0-openai-gateway.md)을 따른다. `codeye doctor`의 버전 확인은 비용을 쓰지 않는다.

- 요청마다 새 작업 디렉터리에 snapshot·요청 digest와 무작위 nonce에 묶인 확인값을 쓴다. OpenCode가 읽은 첨부 파일의 값과 모델 응답의 값을 비교한다. 프롬프트와 선택된 데이터는 표준 입력으로 전달한다.
- `--pure`, 실행별 HOME/TMP, 플랫폼 생성 설정으로 플러그인·업데이트·공유·도구 호출을 차단한다. 사용자 설정과 대상 프로젝트 설정을 읽는 경로를 분리한다. CLI 버전, 모델, 응답 크기, 종료 상태와 cleanup을 확인한다.
- 출력은 한 세션의 완료된 응답만 받는다. 예상 밖의 도구 호출, marker 불일치, 잘린 출력, 시간 초과를 별도 사유로 처리한다. 공개 결과에는 검증된 JSON·모델 및 버전·제공된 usage만 남긴다. reasoning이나 원시 모델 로그는 보고서에 넣지 않는다.
- OpenCode Zen과 명시적으로 구성한 loopback relay가 이 버전의 범위다. relay는 상위 API 키를 관리하며 OpenCode에는 임시 로컬 접근 값만 전달한다. 공급자 변경은 운영자 선택이다.

## 검증 결과

| TC | 실행 | 결과 |
|---|---|---|
| TC-76 | model probe + 실제 OpenCode + 승인된 GPT‑6.2 Sol | 합성 입력 합계 5, 작업별 확인값 일치, verified=true, cleanup=confirmed |
| TC-77 | scripts/test_opencode_policy.py에서 설치된 CLI에 로컬 합성 응답이 read를 요청 | 광고한 도구 0개, 실제 tool_use/read/error, 파일 내용이 모델로 전달되지 않음. 어댑터 회귀 시험에서 같은 이벤트는 policy_blocked |
| TC-78 | 실제 요청이 relay 장부에 예약된 뒤 TestLiveInFlightCancel에서 컨텍스트 취소 | OS 프로세스 트리 정리 확인, cancelled. 이미 전송된 공급자 추론은 계속 청구될 수 있어 장부에서 사용량 또는 보수적인 예약을 유지 |
| TC-79 | 세션 검증, 잘린 JSONL, 잘못된 완료 사유·marker, 미설정/미지원 버전, 모델 예산 경합·재시작 시험 | 유효하지 않은 출력 미게시. relay 시험은 합성 서버 사용 |

무료 제공자 실측에서도 실패를 기록했다. GLM 5.3 Flash Free와 DeepSeek V4.1 Flash Free에서 사용량 제한 또는 timeout이 발생했다. 별도의 합성 연결 시험에서 Nemotron 무료 모델은 timeout, 격리된 CLI에 없는 모델 이름은 오류였다.

이 단계의 실호출 입력은 합성 자료다. 원격 OpenCode attach, Windows/RHEL 모델 실기, 공급자 측 생성의 확정 중단은 미검증이다. 네트워크 차단의 OS 수준 강제는 배포 환경에 의존한다.
