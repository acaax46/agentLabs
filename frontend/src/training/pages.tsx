import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { EmptyState, ErrorPanel, Icon, Loading, Notice, PageHeading } from '../catalog/shared';
import { formatDate } from '../catalog/modules';
import { LazyMarkdown } from '../components/LazyMarkdown';
import { learning } from '../learning/api';
import type { Capabilities } from '../learning/api';
import { openTrainingProblem, training } from './api';
import type {
  TrainingCourse,
  TrainingCourseSummary,
  TrainingProblem,
  TrainingProgress,
} from './api';

const difficultyLabels = { easy: '入门', medium: '进阶', hard: '挑战' };

export function TrainingProblemsPage() {
  const [search, setSearch] = useState('');
  const [difficulty, setDifficulty] = useState('');
  const query = useQuery({
    queryKey: ['training', 'problems', search, difficulty],
    queryFn: () =>
      training<{ items: TrainingProblem[] }>('problems', '', {
        q: search.trim(),
        difficulty,
      }),
  });
  return (
    <>
      <PageHeading
        eyebrow="用户学习空间 / 训练题目"
        title="训练题目"
        description="所有公开题目都可以直接开始；Python 题运行公开样例，HTML 题在工作区使用隔离预览。"
      />
      <form
        className="training-filters"
        onSubmit={(event) => {
          event.preventDefault();
          void query.refetch();
        }}
      >
        <label className="field">
          <span className="field-label">搜索题目</span>
          <input
            type="search"
            value={search}
            placeholder="搜索名称、编号或标签"
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <label className="field">
          <span className="field-label">难度</span>
          <select value={difficulty} onChange={(event) => setDifficulty(event.target.value)}>
            <option value="">全部难度</option>
            <option value="easy">入门</option>
            <option value="medium">进阶</option>
            <option value="hard">挑战</option>
          </select>
        </label>
        <button className="button secondary" type="submit">
          筛选
        </button>
      </form>
      {query.isPending ? (
        <Loading text="正在读取训练题目…" />
      ) : query.isError ? (
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      ) : query.data.items.length === 0 ? (
        <EmptyState
          title="没有匹配的训练题目"
          description="换一个关键词或难度筛选，也可以稍后再来看看。"
          icon="tests"
        />
      ) : (
        <section className="training-list-panel" aria-label="训练题目列表">
          <div className="table-scroll">
            <table className="training-table">
              <thead>
                <tr>
                  <th scope="col">题目</th>
                  <th scope="col">难度</th>
                  <th scope="col">类型</th>
                  <th scope="col">标签</th>
                  <th scope="col" className="actions-column">
                    操作
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.data.items.map((problem) => (
                  <tr key={problem.id}>
                    <td>
                      <Link className="record-title" to={`/student/training/${problem.id}`}>
                        {problem.name}
                      </Link>
                      <div className="record-code">{problem.code}</div>
                    </td>
                    <td>
                      <span className={`difficulty-badge ${problem.difficulty}`}>
                        {difficultyLabels[problem.difficulty]}
                      </span>
                    </td>
                    <td>{problem.language === 'html' ? 'HTML 预览' : 'Python'}</td>
                    <td>
                      <div className="training-tags">
                        {problem.tags.slice(0, 3).map((tag) => (
                          <span key={tag}>{tag}</span>
                        ))}
                      </div>
                    </td>
                    <td>
                      <Link to={`/student/training/${problem.id}`}>查看题目</Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </>
  );
}

export function TrainingProblemPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: ['training', 'problem', id],
    queryFn: () => training<TrainingProblem>('problem', id),
    enabled: !!id,
  });
  const capabilities = useQuery({
    queryKey: ['learning', 'capabilities'],
    queryFn: () => learning<Capabilities>('capabilities'),
    staleTime: 15_000,
  });
  const open = useMutation({
    mutationFn: () => openTrainingProblem(id),
    onSuccess: (workspace) => navigate(`/workspace/${workspace.id}`),
  });
  if (query.isPending) return <Loading text="正在读取训练题目…" />;
  if (query.isError) return <ErrorPanel error={query.error} retry={() => void query.refetch()} />;
  const problem = query.data;
  const isHtml = problem.language === 'html';
  const unavailable = capabilities.data && !capabilities.data.publicTest;
  return (
    <>
      <Link className="back-link" to="/student/training">
        返回训练题目
      </Link>
      <PageHeading
        eyebrow="训练题目 / 公开题库"
        title={`${problem.code} · ${problem.name}`}
        description={
          isHtml
            ? `${difficultyLabels[problem.difficulty]} · HTML / CSS / JavaScript · 浏览器隔离预览`
            : `${difficultyLabels[problem.difficulty]} · ${problem.timeLimitMs} ms · ${problem.memoryLimitMb} MB`
        }
        action={
          <button className="button" onClick={() => open.mutate()} disabled={open.isPending}>
            {open.isPending ? '正在打开代码空间…' : '开始训练'}
          </button>
        }
      />
      {open.error && <ErrorPanel error={open.error} />}
      {isHtml ? (
        <Notice>
          <strong>本题使用隔离预览</strong>
          <p>
            开始训练后在代码空间点击“预览页面”，即可即时查看 HTML/CSS/JavaScript 效果；本题不进入
            stdin/stdout 评测。
          </p>
        </Notice>
      ) : (
        unavailable && (
          <Notice tone="warning">
            <strong>沙箱执行器尚未就绪</strong>
            <p>{capabilities.data?.reason || '可以编辑和保存代码，但暂时不能运行公开样例。'}</p>
          </Notice>
        )
      )}
      <div className="training-detail">
        <section className="learning-panel training-statement">
          <LazyMarkdown value={problem.statement} />
          <h3>输入说明</h3>
          <LazyMarkdown value={problem.inputDescription} />
          <h3>输出说明</h3>
          <LazyMarkdown value={problem.outputDescription} />
          <p className="field-help">题目来源：{problem.source}</p>
        </section>
        <section className="learning-panel">
          <h2>{isHtml ? '页面预览' : '公开样例'}</h2>
          {isHtml ? (
            <p className="field-help">
              开始训练后，在代码空间点击“预览页面”。请把样式写在 styles.css，交互脚本写在
              script.js。
            </p>
          ) : problem.samples.length ? (
            problem.samples.map((sample, index) => (
              <div className="sample-grid" key={index}>
                <div>
                  <h3>样例 {index + 1} 输入</h3>
                  <pre>{sample.input || '（空输入）'}</pre>
                </div>
                <div>
                  <h3>样例 {index + 1} 输出</h3>
                  <pre>{sample.output}</pre>
                </div>
              </div>
            ))
          ) : (
            <p className="field-help">这道题暂时没有公开样例。</p>
          )}
        </section>
      </div>
    </>
  );
}

export function TrainingCoursesPage() {
  const query = useQuery({
    queryKey: ['training', 'courses'],
    queryFn: () => training<{ items: TrainingCourseSummary[] }>('courses'),
  });
  return (
    <>
      <PageHeading
        eyebrow="用户学习空间 / 课程资源"
        title="课程资源"
        description="由管理员按班级分配的课程文档，按小节连贯学习，完成状态会保存在账号中。"
      />
      {query.isPending ? (
        <Loading text="正在读取课程资源…" />
      ) : query.isError ? (
        <ErrorPanel error={query.error} retry={() => void query.refetch()} />
      ) : query.data.items.length === 0 ? (
        <EmptyState
          title="暂无课程资源"
          description="管理员分配课程后，会出现在这里。"
          icon="courses"
        />
      ) : (
        <section className="training-grid" aria-label="课程资源列表">
          {query.data.items.map((course) => {
            const percent = course.sectionCount
              ? Math.round((course.completedSections / course.sectionCount) * 100)
              : 0;
            return (
              <Link
                className="training-card"
                key={course.id}
                to={`/student/resources/${course.id}`}
              >
                <div className="training-card-top">
                  <Icon name="courses" />
                  <span className="status-badge active">
                    {course.completedSections}/{course.sectionCount} 节
                  </span>
                </div>
                <h2>{course.name}</h2>
                <p>{course.description}</p>
                <div
                  className="progress-track"
                  role="progressbar"
                  aria-label="课程完成度"
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={percent}
                >
                  <span style={{ width: `${percent}%` }} />
                </div>
                <span className="training-card-action">
                  {course.completedSections ? '继续学习' : '开始学习'}
                </span>
                <span className="field-help">更新于 {formatDate(course.updatedAt)}</span>
              </Link>
            );
          })}
        </section>
      )}
    </>
  );
}

export function TrainingCoursePage() {
  const { id = '' } = useParams();
  const cache = useQueryClient();
  const query = useQuery({
    queryKey: ['training', 'course', id],
    queryFn: () => training<TrainingCourse>('course', id),
    enabled: !!id,
  });
  const problems = useQuery({
    queryKey: ['training', 'problems', '', ''],
    queryFn: () => training<{ items: TrainingProblem[] }>('problems'),
    staleTime: 60_000,
  });
  const [active, setActive] = useState('');
  const [notice, setNotice] = useState('');
  useEffect(() => {
    if (!query.data) return;
    if (!active || !query.data.sections.some((section) => section.id === active)) {
      setActive(query.data.progress.lastSectionId || query.data.sections[0]?.id || '');
    }
  }, [query.data, active]);
  const save = useMutation({
    mutationFn: ({
      sectionId,
      completed,
    }: {
      sectionId: string;
      completed: boolean;
      silent?: boolean;
    }) => training<TrainingProgress>('progress', id, { sectionId, completed }),
    onSuccess: (progress, variables) => {
      cache.setQueryData(['training', 'course', id], (current: TrainingCourse | undefined) =>
        current
          ? {
              ...current,
              progress,
              completedSections: progress.completedSectionIds.length,
            }
          : current,
      );
      void cache.invalidateQueries({ queryKey: ['training', 'courses'] });
      if (!variables.silent)
        setNotice(variables.completed ? '本节已标记完成。' : '已取消完成标记。');
    },
  });
  const selectSection = (sectionId: string) => {
    setActive(sectionId);
    setNotice('');
    const current = query.data;
    if (!current) return;
    save.mutate({
      sectionId,
      completed: current.progress.completedSectionIds.includes(sectionId),
      silent: true,
    });
  };
  if (query.isPending) return <Loading text="正在读取课程…" />;
  if (query.isError) return <ErrorPanel error={query.error} retry={() => void query.refetch()} />;
  const course = query.data;
  const section = course.sections.find((item) => item.id === active) || course.sections[0];
  if (!section)
    return (
      <EmptyState
        title="课程还没有小节"
        description="课程内容准备后会显示在这里。"
        icon="courses"
      />
    );
  const completed = course.progress.completedSectionIds.includes(section.id);
  const index = course.sections.findIndex((item) => item.id === section.id);
  const linkedProblems = section.problemIds
    .map((problemId) => problems.data?.items.find((problem) => problem.id === problemId))
    .filter((problem): problem is TrainingProblem => !!problem);
  const percent = course.sections.length
    ? Math.round((course.progress.completedSectionIds.length / course.sections.length) * 100)
    : 0;
  return (
    <>
      <Link className="back-link" to="/student/resources">
        返回课程资源
      </Link>
      <PageHeading
        eyebrow="课程资源 / 私有课程"
        title={course.name}
        description={course.description}
      />
      <div className="course-reader">
        <aside className="course-outline" aria-label="课程小节">
          <div className="course-progress">
            <strong>
              已完成 {course.progress.completedSectionIds.length}/{course.sections.length} 节
            </strong>
            <div
              className="progress-track"
              role="progressbar"
              aria-label="课程完成度"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={percent}
            >
              <span style={{ width: `${percent}%` }} />
            </div>
          </div>
          <ol>
            {course.sections.map((item, sectionIndex) => {
              const done = course.progress.completedSectionIds.includes(item.id);
              return (
                <li key={item.id}>
                  <button
                    type="button"
                    className={item.id === section.id ? 'active' : ''}
                    aria-current={item.id === section.id ? 'page' : undefined}
                    onClick={() => selectSection(item.id)}
                  >
                    <span>
                      {sectionIndex + 1}. {item.title}
                    </span>
                    {done && <small>已完成</small>}
                  </button>
                </li>
              );
            })}
          </ol>
        </aside>
        <article className="course-section">
          <div className="course-section-head">
            <span className="eyebrow">
              第 {index + 1} 节 / 共 {course.sections.length} 节
            </span>
            <span className={`status-badge ${completed ? 'active' : 'draft'}`}>
              {completed ? '已完成' : '学习中'}
            </span>
          </div>
          <h2>{section.title}</h2>
          <LazyMarkdown value={section.document} />
          {linkedProblems.length > 0 && (
            <section className="course-practice">
              <h3>本节练习</h3>
              <div className="training-tags">
                {linkedProblems.map((problem) => (
                  <Link key={problem.id} to={`/student/training/${problem.id}`}>
                    {problem.code} · {problem.name}
                  </Link>
                ))}
              </div>
            </section>
          )}
          {save.error && <ErrorPanel error={save.error} />}
          {notice && <p role="status">{notice}</p>}
          <div className="course-actions">
            <button
              className="button secondary"
              type="button"
              disabled={index <= 0}
              onClick={() => selectSection(course.sections[index - 1]!.id)}
            >
              上一节
            </button>
            <button
              className="button secondary"
              type="button"
              disabled={index >= course.sections.length - 1}
              onClick={() => selectSection(course.sections[index + 1]!.id)}
            >
              下一节
            </button>
            <button
              className="button"
              type="button"
              disabled={save.isPending}
              onClick={() => save.mutate({ sectionId: section.id, completed: !completed })}
            >
              {save.isPending ? '正在保存…' : completed ? '取消完成标记' : '标记本节已完成'}
            </button>
          </div>
          <p className="field-help">学习进度保存在账号中，刷新或重新登录后仍会保留。</p>
        </article>
      </div>
    </>
  );
}
