use smallvec::SmallVec;

#[test]
fn extend_past_inline_capacity_keeps_every_item() {
    let mut v: SmallVec<u32, 3> = SmallVec::new();
    v.push(100);
    v.extend((1..=12u32).filter(|n| n % 2 == 0));
    let expected: Vec<u32> = vec![100, 2, 4, 6, 8, 10, 12];
    assert_eq!(v.as_slice(), expected.as_slice());
}

#[test]
fn collect_from_unsized_iterator_keeps_every_item() {
    let words = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"];
    let v: SmallVec<String, 2> = words.iter().filter(|w| w.len() > 0).map(|w| w.to_string()).collect();
    let got: Vec<&str> = v.iter().map(|s| s.as_str()).collect();
    assert_eq!(got, words.to_vec());
}
