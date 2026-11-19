fn main() {
    std::fs::write("src/lib.rs", "pub fn rewritten() {}\n").unwrap();
}
