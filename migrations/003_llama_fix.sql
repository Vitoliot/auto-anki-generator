-- примерная идея, подгони под свою схему
ALTER TABLE chunks
  ALTER COLUMN embedding TYPE vector(768);

-- затем пересчитать embeddings для всех chunks
-- (через отдельную джобу/скрипт)