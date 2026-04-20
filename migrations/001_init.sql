-- 001_init.sql
-- ERD + пару технических колонок для прототипа (storage_path).
create extension if not exists "uuid-ossp";
create extension if not exists "vector";

create table if not exists users (
  id uuid primary key,
  email varchar(255) not null unique,
  password_hash varchar(255) not null,
  name varchar(255) not null,
  role varchar(32) not null check (role in ('user','admin')),
  created_at timestamptz not null default now(),
  last_login_at timestamptz
);

create table if not exists sources (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  title varchar(255) not null,
  type varchar(32) not null check (type in ('pdf','docx','html','markdown','text')),
  original_filename varchar(255) not null,
  language varchar(8),
  status varchar(16) not null check (status in ('uploaded','parsed','indexed','error')),
  page_count int,
  storage_path text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_sources_user on sources(user_id);
create index if not exists idx_sources_status on sources(status);

create table if not exists chunks (
  id uuid primary key,
  source_id uuid not null references sources(id) on delete cascade,
  ord int not null,
  section_path varchar(255),
  text text not null,
  token_count int,
  embedding vector(1536),
  created_at timestamptz not null default now(),
  unique (source_id, ord)
);

-- pgvector index (IVFFLAT требует ANALYZE и разумного lists)
-- В проде: создать после загрузки данных и подобрать lists по объёму.
create index if not exists idx_chunks_source on chunks(source_id);
create index if not exists idx_chunks_embedding on chunks using ivfflat (embedding vector_cosine_ops) with (lists = 100);

create table if not exists card_sets (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  title varchar(255) not null,
  description text,
  origin varchar(32) not null check (origin in ('manual','generated')),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_card_sets_user on card_sets(user_id);

create table if not exists generation_jobs (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  source_id uuid not null references sources(id) on delete cascade,
  card_set_id uuid references card_sets(id) on delete set null,
  status varchar(16) not null check (status in ('pending','running','ready','failed')),
  requested_at timestamptz not null default now(),
  started_at timestamptz,
  finished_at timestamptz,
  card_type varchar(16) not null check (card_type in ('basic','cloze','mixed')),
  max_cards int not null,
  target_language varchar(8),
  model_name varchar(64),
  temperature numeric(3,2),
  top_k int,
  seed int,
  total_cards_generated int not null default 0,
  total_cards_approved int not null default 0,
  duplicate_count int not null default 0,
  avg_answer_length int not null default 0,
  coverage_score numeric(4,3)
);

create index if not exists idx_gen_jobs_user on generation_jobs(user_id);
create index if not exists idx_gen_jobs_status on generation_jobs(status);
create index if not exists idx_gen_jobs_source on generation_jobs(source_id);

create table if not exists cards (
  id uuid primary key,
  card_set_id uuid references card_sets(id) on delete set null,
  generation_job_id uuid references generation_jobs(id) on delete set null,
  user_id uuid not null references users(id) on delete cascade,
  type varchar(16) not null check (type in ('basic','cloze')),
  question text not null,
  answer text not null,
  extra text,
  status varchar(16) not null check (status in ('draft','approved','rejected')),
  difficulty int,
  srs_interval int,
  srs_due_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_cards_user on cards(user_id);
create index if not exists idx_cards_job on cards(generation_job_id);
create index if not exists idx_cards_set on cards(card_set_id);
create index if not exists idx_cards_status on cards(status);

create table if not exists tags (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  name varchar(64) not null,
  created_at timestamptz not null default now(),
  unique(user_id, name)
);

create table if not exists card_tags (
  card_id uuid not null references cards(id) on delete cascade,
  tag_id uuid not null references tags(id) on delete cascade,
  primary key (card_id, tag_id)
);

create table if not exists card_source_links (
  card_id uuid not null references cards(id) on delete cascade,
  chunk_id uuid not null references chunks(id) on delete cascade,
  relevance numeric(4,3),
  primary key (card_id, chunk_id)
);

create table if not exists export_jobs (
  id uuid primary key,
  user_id uuid not null references users(id) on delete cascade,
  card_set_id uuid not null references card_sets(id) on delete cascade,
  format varchar(16) not null check (format in ('csv','apkg')),
  status varchar(16) not null check (status in ('pending','running','ready','failed')),
  file_path varchar(255),
  created_at timestamptz not null default now(),
  finished_at timestamptz,
  error_message text
);

create index if not exists idx_export_jobs_user on export_jobs(user_id);
create index if not exists idx_export_jobs_status on export_jobs(status);
