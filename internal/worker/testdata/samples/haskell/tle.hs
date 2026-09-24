loop :: Int -> Int
loop n = if n < 0 then n else loop (n + 1)
main :: IO ()
main = print (loop 1)
