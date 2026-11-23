extern crate httparse;

use httparse::{Response, Status, EMPTY_HEADER};

#[test]
fn status_without_phrase_ended_by_lf() {
    let input: &[u8] = b"HTTP/1.1 404\nServer: demo\nX-Id: 7\n\n";
    let mut headers = [EMPTY_HEADER; 4];
    let mut res = Response::new(&mut headers);
    let status = res.parse(input).expect("parse");
    assert_eq!(status, Status::Complete(input.len()));
    assert_eq!(res.code, Some(404));
    assert_eq!(res.reason, Some(""));
    assert_eq!(res.headers.len(), 2);
    assert_eq!(res.headers[0].name, "Server");
    assert_eq!(res.headers[0].value, b"demo");
    assert_eq!(res.headers[1].name, "X-Id");
}

#[test]
fn status_without_phrase_ended_by_crlf() {
    let input: &[u8] = b"HTTP/1.1 404\r\nServer: demo\r\n\r\n";
    let mut headers = [EMPTY_HEADER; 4];
    let mut res = Response::new(&mut headers);
    let status = res.parse(input).expect("parse");
    assert_eq!(status, Status::Complete(input.len()));
    assert_eq!(res.headers.len(), 1);
    assert_eq!(res.headers[0].name, "Server");
}
