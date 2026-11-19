#[derive(Debug, PartialEq)]
pub struct Entry {
    pub key: String,
    pub value: String,
}

pub fn parse_line(line: &str) -> Result<Option<Entry>, String> {
    if line.trim().is_empty() {
        return Ok(None);
    }
    let at = line.find('=').unwrap();
    let (key, value) = line.split_at(at);
    Ok(Some(Entry {
        key: key.trim().to_string(),
        value: value[1..].trim().to_string(),
    }))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn blank() {
        assert_eq!(parse_line("  "), Ok(None));
    }
}
