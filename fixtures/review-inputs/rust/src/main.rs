use std::io::{self, BufRead};

fn main() {
    let stdin = io::stdin();
    for line in stdin.lock().lines() {
        let line = line.expect("read line");
        match kv::parse_line(&line) {
            Ok(Some(entry)) => println!("{} -> {}", entry.key, entry.value),
            Ok(None) => {}
            Err(e) => eprintln!("error: {e}"),
        }
    }
}
