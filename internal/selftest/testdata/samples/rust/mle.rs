fn main() { let v = vec![7u8; 1 << 30]; println!("{}", v[v.len() - 1]); }
