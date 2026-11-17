# 합성 fixture: 콜백 실패와 임시 디렉터리 정리

이 폴더는 CODEYE의 근거 계약과 평가 방식을 설명하는 작은 예제다. 사용자 프로젝트의 실제 결함이나 CODEYE 분석기의 실행 결과가 아니다.

- `before.py`: 콜백이 예외를 던지면 정상 경로 뒤의 rmtree가 실행되지 않는다.
- `after.py`: finally에서 정리하는 기본 수정 예시다.
- `test_cleanup.py`: 결함 전후의 기대 동작을 검사한다. buggy 코드의 결함을 확인하는 테스트는 디렉터리가 남는 것을 assert하므로 전체 테스트가 통과할 수 있다.
- `expectations.json`: 평가 정답이다. 분석 대상에 포함하지 않는다.

```bash
python -m unittest discover -s fixtures/cleanup -p 'test_*.py' -v
```

부모 임시 디렉터리는 테스트 종료 시 정리되어 실제 사용자 경로를 건드리지 않는다. six test methods는 정상 cleanup, 실패 전후, 반환값·예외 전달, BaseException 경로를 다룬다. 이는 모든 OS·실제 취소 체계·cleanup 자체 실패를 검증한다는 뜻은 아니다.

## 분석 평가에 사용할 때

before 또는 after 중 하나만 별도의 임시 프로젝트에 `job.py`라는 중립 이름으로 복사한다. README·expectations·반대 variant·테스트 정답 설명은 모델에 제공하지 않는다. 평가기는 외부 정답을 보관하고 CODEYE 결과가 원인·조건·근거를 맞췄는지 판정한다.

## 의도적으로 남긴 한계

finally의 shutil.rmtree 자체가 실패하면 원래 예외를 덮을 수 있다. 수정 예시는 이 정책까지 해결한 프로덕션 구현이 아니다. 실제 프로젝트에서는 자원 소유권·cleanup 오류 보존·재시도·운영 정책을 결정해야 한다. 이 fixture만으로 전체 분석 정확도·오탐률을 발표하지 않는다.
