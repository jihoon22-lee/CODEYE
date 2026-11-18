# 16. 분석 품질 평가와 회귀 검증

## 1. 평가 목적

많은 경고, 긴 보고서, 많은 모델 호출을 품질로 보지 않는다. 실제로 중요한 문제를 찾아내는가, 정상 동작을 잘못 지적하지 않는가, 근거가 맞는가, 사용자가 조치할 수 있는가를 평가한다. 이 문서에는 전체 제품의 평가 방법을 정리했다. Go 개인 모드의 첫 측정 결과와 표본 범위는 [R0 파일럿](36-r0-pilot.md)에 있다.

## 2. 데이터셋

| 집합 | 구성 | 용도 |
|---|---|---|
| 합성 결함 쌍 | 결함 전/수정 후, 동일 API, 발생 조건·정답·반례 | 알려진 결함 발견과 수정 후 오탐 |
| 정상/까다로운 코드 | 상위 방어, 소유권 이전, 의도된 예외, 비동기 종료 | 과도한 주장 억제 |
| 실제 결함 수정 | 사용 허가된 개인 프로젝트의 수정 전후 | 현실적인 구조·맥락 검토 |
| 레거시 프로젝트 | 문서 없음, 불완전 build, mixed versions | 부분 분석·공백 전달 |
| 공격/장애 fixture | prompt injection, malformed output, invalid path, timeout | 실행·근거 경계 |
| 보고서 읽기 과제 | 동일 결과를 다른 구조로 표현 | 이해도·우선순위 판단 |

초기 R0는 최소 10개 결함/정상 쌍, R1은 언어별 10개 이상 쌍을 목표로 한다. 이는 충분한 통계 검증을 보장하는 수가 아니라 구성 목표다. 라이선스·비공개 코드 반출 권한을 확인한다. 현재 포함한 cleanup fixture 하나만으로 전체 품질을 평가했다고 하지 않는다.

튜닝 집합과 보류 집합은 프로젝트/원인 family 기준으로 나눈다. 같은 결함의 변수명만 바꾼 쌍을 양쪽에 배치하지 않는다. 정답, 기대 finding, 수정 후 설명은 분석 대상 snapshot 밖에 둔다. 프롬프트와 검색 대상에 정답이 섞이면 해당 실험을 무효로 한다.

## 3. 정답 라벨

각 사례는 ID, language, source digest, defect family, trigger, expected impact, positive locations, known counterevidence, corrected variant, reviewer notes를 갖는다. expected message와 문구가 다르다는 이유만으로 오답 처리하지 않고 원인·조건·영향이 같은지 평가한다.

사람 판정은 valid_actionable, valid_informational, false_positive, duplicate, insufficient_evidence, out_of_scope로 나눈다. 중요한 사례는 독립 검토자와 불일치 조정 절차를 둔다. 사용자 dismissal을 자동 오탐 정답으로 쓰지 않는다.

## 4. 지표와 분모

| 지표 | 정의와 제한 |
|---|---|
| actionable precision | 판정된 actionable 후보 중 실제 유효한 비율. 미판정은 제외하고 수를 따로 표시 |
| known-defect recall | 알려진 평가 결함 중 원인/조건을 올바르게 포착한 비율. 일반 프로젝트의 모든 미지 결함 recall을 의미하지 않음 |
| false-positive rate | 명시적 정상 사례 중 잘못 결함을 주장한 비율. 알려지지 않은 코드 전체를 정상 분모로 쓰지 않음 |
| evidence validity | 존재하는 path/range/hash/실행 근거를 정확히 연결한 주장 비율 |
| unsupported assertion rate | 근거가 없거나 증거보다 강한 결론의 비율 |
| duplicate burden | 동일 원인 중복 카드 수와 검토 시간 부담 |
| reversal quality | 반례가 있는 후보를 철회하고 실제 결함은 유지하는지 |
| report usefulness | 영향·조건·수정/확인 방법을 독자가 정확히 찾는지 |
| resource cost | wall time·CPU/RAM·모델 호출/제공된 usage, 캐시 여부 |

precision은 점 추정만 제시하지 않고 표본 수·불확실성 구간·언어/원인별 편차를 보고한다. 비용·속도와 정확도를 하나의 임의 점수로 합치지 않는다. 지표 개선을 위해 모든 발견을 숨기는 모델은 recall과 미확정 비율로 드러나야 한다.

## 5. 실험 구성

도구만, LLM 맥락 검토만, 도구+LLM, 도구+LLM+반증을 동일 snapshot과 예산 조건에서 비교한다. 전체 token/call/time 예산 차이를 명시하고 단순히 더 많은 비용을 쓴 조합을 지침 효과라고 하지 않는다. 비결정적 모델은 대표 사례를 반복 실행해 분산을 본다.

도구·rule/pack digest, 모델 endpoint identity/revision(가용 시), adapter version, context selection, temperature 등 제공 설정, OS/runtime, 시간 제한, 캐시를 기록한다. 변경된 요소가 여러 개면 효과를 분리할 추가 실험을 계획한다.

## 6. 출시 판단

출판되는 근거 참조의 구조적 유효성은 100%를 요구한다. 유효하지 않은 참조는 숨기고 제한을 표시해야 하므로 무효 링크를 그대로 게시하는 것은 출시 차단이다. 실행 근거 없는 reproduced, 검사 실패를 0건 통과로 표시, 다른 프로젝트 데이터 노출, late result 덮어쓰기도 차단한다.

분석 precision/recall 컷은 첫 라벨링 baseline의 표본·분산을 보고 결정 기록에 확정한다. 측정 전 임의 정확도 수치를 보장하지 않는다. 지침 변경은 중요한 기존 사례의 회귀를 검토하고 승인되지 않은 성능 저하가 있으면 기본 팩 승격을 막는다.

## 7. 보고서 평가

독자에게 중요한 위험 3개, 각 발생 조건, 근거 위치, 분석하지 못한 범위, 다음 조치를 찾도록 한다. 답의 정확성·추가 질문 수·소요 시간을 측정한다. 예쁜 차트 수나 문장 길이는 대리 지표가 아니다. 모호한 심각도, 중복, 수치 불일치, 과장된 단정은 결함으로 기록한다.

## 8. 현재 예제의 역할

[cleanup fixture](../fixtures/cleanup/README.md)는 실행 가능한 합성 결함과 수정 후 코드를 제공한다. fixture 테스트 통과는 결함 전후의 기대 동작을 확인하는 것이며 CODEYE 분석기가 이 결함을 찾았다는 뜻이 아니다. 실제 플랫폼 회귀 테스트는 구현 후 동일 입력을 제품 파이프라인에 넣고 finding을 평가해야 한다.
