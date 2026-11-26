// Builds the crate's output-file reader and its JSON summary from the work copy and checks the
// overall status for produced, missing and mixed output lists.
#[allow(dead_code)]
#[path = "../../mistralrs-code-exec/src/protocol.rs"]
mod protocol;

#[allow(dead_code)]
#[path = "../../mistralrs-code-exec/src/files.rs"]
mod files;

use protocol::ExecuteOutputSpec;
use std::path::Path;

fn spec(name: &str) -> ExecuteOutputSpec {
    ExecuteOutputSpec { name: name.to_string(), format: None }
}

fn summary(dir: &Path, names: &[&str]) -> serde_json::Value {
    let outputs: Vec<_> = names.iter().map(|n| spec(n)).collect();
    let files = files::collect_output_files(dir, &outputs);
    serde_json::from_str(&files::surface_outputs_json(dir, &files)).expect("summary is JSON")
}

fn main() {
    let dir = std::env::current_dir().unwrap().join("outputs-status-work");
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    std::fs::write(dir.join("deck.pptx"), b"pptx bytes").unwrap();
    std::fs::write(dir.join("notes.txt"), "plain text").unwrap();

    let cases: [(&str, &[&str], &str); 3] = [
        ("all produced", &["deck.pptx", "notes.txt"], "success"),
        ("all missing", &["missing.html", "gone.csv"], "error"),
        ("one produced, one missing", &["deck.pptx", "missing.html"], "error"),
    ];
    let mut failed = 0;
    for (what, names, want) in cases {
        let json = summary(&dir, names);
        let per_file: Vec<_> = json["files"].as_array().unwrap().iter().map(|f| f["status"].clone()).collect();
        println!("{what}: status={} files={:?}", json["status"], per_file);
        if json["status"] != want {
            println!("OUTPUT STATUS CHECK FAILED: {what}: overall status {} but expected {want}", json["status"]);
            failed += 1;
        }
    }
    let _ = std::fs::remove_dir_all(&dir);
    if failed > 0 {
        std::process::exit(1);
    }
    println!("output status check passed");
}
