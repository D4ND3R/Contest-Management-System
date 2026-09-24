use std::io::Read;
fn main() { let mut s = String::new(); std::io::stdin().read_to_string(&mut s).unwrap(); let v: Vec<i64> = s.split_whitespace().map(|x| x.parse().unwrap()).collect(); println!("{}", v[0] - v[1]); }
