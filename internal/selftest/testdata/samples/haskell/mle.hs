import qualified Data.Map.Strict as M
main :: IO ()
main = print (M.size (M.fromList [(i, i) | i <- [1 .. 100000000 :: Int]]))
