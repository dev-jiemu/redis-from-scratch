# redis-from-scratch

Redis를 Go로 직접 구현하며 내부 동작을 이해하는 학습 프로젝트.

- 호환 목표: `redis-cli`, `redis-benchmark` 접속 (RESP2)

## 구조

설계의 5개 레이어와 패키지가 1:1로 대응한다. 의존 방향은 `network → protocol → command → store → persist` 단방향.

| 패키지 | 역할 |
|--------|------|
| `cmd/server` | 서버 진입점 |
| `internal/network` | TCP 서버, (추후) event loop |
| `internal/protocol` | RESP2 parser / writer |
| `internal/command` | command table, dispatcher |
| `internal/store` | DB, object 타입, expiry |
| `internal/persist` | RDB, AOF |

## 개발

```
make build   # go build ./...
make test    # go test -race ./...
make bench   # go test -bench=. -benchmem ./...
```

## 진행 현황

| Phase | 작업 | 상태 |
|-------|------|------|
| 0 | 프로젝트 셋업 (모듈, 패키지 뼈대, Makefile) | ✅ 완료 |
| 1-1 | RESP2 파서 (부분 수신 처리, 프로토콜 에러 구분) | ✅ 완료 |
| 1-2 | RESP writer (parser와 round-trip 검증) | ✅ 완료 |
| 1-3 | TCP 서버 (conn당 goroutine) + 단일 executor | ✅ 완료 |
| 1-4 ~ 1-5 | command table / dispatcher, in-memory store | ⏭️ 다음 작업 |
| 2 ~ 7 | 자료구조, TTL, RDB/AOF, 심화 기능, event loop 교체, 인코딩 최적화 | ⏳ 예정 |

### 다음에 이어서 할 작업

**1-4 command table / dispatcher** (`PING`, `ECHO`, `GET`, `SET`, `DEL`, `EXISTS`, 알 수 없는 명령은 `-ERR unknown command`)

- **소견**: 1-5(in-memory `map[string]*Object`)와 **같이 진행**하는 것을 권장한다. `GET`/`SET`/`DEL`/`EXISTS`는 store 없이는 의미 있는 테스트를 쓸 수 없고, dispatcher가 store 인터페이스를 어떻게 쓰는지가 함께 정해져야 하기 때문이다.
- **완료 기준**: `redis-cli`로 `SET k v` → `GET k` 동작, `redis-benchmark -t set,get -q` 에러 없이 완주 (이 시점에 `brew install redis`로 클라이언트 도구 설치 필요).
- **결정 완료**: `Handler` panic은 executor에서 `recover` 후 `-ERR internal error`로 응답하고 서버는 유지한다(스택은 로그). 에러/Simple String의 CR/LF는 writer가 공백으로 치환한다.
- **⚠️ panic 복구의 한계**: 명령이 store를 일부만 바꾼 채 panic하면 상태가 불일치할 수 있다. 1-4/1-5에서 "검증 후 변경" 순서로 작성해 완화한다.

### 구현 메모

- **파서 (`internal/protocol/parser.go`)**: `Parse(buf)`는 메시지가 덜 도착했으면 `ErrIncomplete`와 `consumed=0`을 반환한다. 호출자는 버퍼를 그대로 두고 더 읽으면 된다(Redis의 query buffer 역할). 잘못된 입력은 `*ProtocolError`로 구분한다.
- **writer (`internal/protocol/writer.go`)**: `Append(dst, v)`는 호출자 버퍼에 이어 붙인다. 여러 응답을 한 버퍼에 모아 한 번에 write하는 pipeline 응답 flush의 기반이다.
- **서버 (`internal/network/server.go`)**: 연결마다 goroutine이 읽기·파싱을 맡고, 명령 실행은 서버당 하나인 executor goroutine이 채널로 받아 직렬화한다. 한 번에 읽은 명령들의 응답은 모아서 write 한 번으로 보낸다. `network`는 `Handler` 인터페이스에만 의존한다(실제 구현은 1-4).
- **종료 순서**: `Close()` → listener/연결 닫기 → 연결 goroutine 종료 대기 → 요청 채널 close → executor 종료 대기.
- **알려진 제한**: inline command(평문 명령) 미지원, 불완전한 큰 요청(큰 bulk·큰 array)을 조각으로 받을 때 매번 처음부터 재파싱(O(n²), 20만 원소 array를 16KB씩 받으면 한 번에 파싱할 때의 약 60배 측정).
- **중첩 깊이 제한**: 파서는 array 중첩을 `maxNestingDepth`(32)까지만 허용한다. 제한이 없으면 `*1\r\n` 반복 입력으로 goroutine 스택 한도를 넘어 recover 불가능한 fatal error 로 프로세스가 죽는다.
- **query buffer 상한**: 연결당 미완성으로 남은 query가 `defaultQueryLimit`(1GB, Redis `client-query-buffer-limit`과 동일)을 넘으면 `-ERR` 응답 후 연결을 끊는다(완결된 명령에는 적용되지 않음). 처리 후 cap이 `idleQueryCap`(64KB)보다 큰 버퍼가 거의 비면(남은 길이 ≤ cap/4) 새 슬라이스로 교체해 메모리를 반납한다(`compactQuery`).
- **⚠️ 확인 필요**: 에러 응답 직후 `return`으로 연결을 닫을 때, 수신 버퍼에 읽지 않은 데이터가 남아 있으면 TCP RST가 나가 클라이언트가 에러 응답을 못 읽을 수 있다(미재현).
- 상세 분석: [`docs/summary/20261025-server-close-and-query-buffer-summary.md`](docs/summary/20261025-server-close-and-query-buffer-summary.md)
