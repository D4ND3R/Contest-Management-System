main :: IO ()
main = do
  [a, b] <- map read . words <$> getLine :: IO [Integer]
  print (a - b)
