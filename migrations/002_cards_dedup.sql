-- 002_cards_dedup.sql
-- "Дипломный" апгрейд: идемпотентность генерации при retry/DLQ.
-- Добавляем вычисляемый хэш контента карточки и уникальность в рамках generation_job_id.
--
-- Важно: generation_job_id может быть NULL (ручные карточки). В Postgres NULL-значения
-- не конфликтуют в UNIQUE, поэтому ручные карточки не ограничиваем.

alter table cards
  add column if not exists content_hash text
  generated always as (
    md5(lower(btrim(question)) || '||' || lower(btrim(answer)))
  ) stored;

do $$
begin
  if not exists (
    select 1
    from   pg_constraint
    where  conname = 'uq_cards_generation_job_content_hash'
  ) then
    alter table cards
      add constraint uq_cards_generation_job_content_hash
      unique (generation_job_id, content_hash);
  end if;
end$$;
