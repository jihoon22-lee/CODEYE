use kv::parse_line;

#[test]
fn key_value() {
    let e = parse_line("a = 1").unwrap().unwrap();
    assert_eq!(e.key, "a");
    assert_eq!(e.value, "1");
}
