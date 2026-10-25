package training

import "agentlab.local/backend/internal/catalog"

const trainingSource = "参考 NOAI Club 备赛资源主题，由 AgentLab 改写并补充样例。"

func demoProblems() []Problem {
	problems := []Problem{
		{
			ID: "train-print-01", Code: "T-PRINT-01", Name: "输出第一行代码", Difficulty: "easy",
			Tags: []string{"Python", "输出"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 1,
			Statement:        "## 输出第一行代码\n\n使用 Python 输出题目要求的文本。本题不需要读取输入。\n\n> 这是训练题库的第一题，重点检查输出内容是否完全一致。",
			InputDescription: "无输入。", OutputDescription: "输出一行：Hello, AgentLab!",
			Samples:     []catalog.Sample{{Input: "", Output: "Hello, AgentLab!\n", Explanation: "注意标点、空格和大小写。"}},
			InitialCode: "# 使用 print() 输出题目要求的文本\n",
			Source:      trainingSource,
		},
		{
			ID: "train-arith-02", Code: "T-ARITH-02", Name: "两个整数的和与差", Difficulty: "easy",
			Tags: []string{"Python", "变量", "算术"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 2,
			Statement:        "## 两个整数的和与差\n\n读入两个整数 a 和 b，第一行输出 a + b，第二行输出 a - b。",
			InputDescription: "一行两个整数 a、b。", OutputDescription: "两行，依次为和与差。",
			Samples: []catalog.Sample{
				{Input: "3 5\n", Output: "8\n-2\n", Explanation: "3 + 5 = 8，3 - 5 = -2。"},
				{Input: "10 4\n", Output: "14\n6\n", Explanation: ""},
			},
			InitialCode: "a, b = map(int, input().split())\n# 输出 a + b 和 a - b\n",
			Source:      trainingSource,
		},
		{
			ID: "train-cond-03", Code: "T-COND-03", Name: "三个数的最大值", Difficulty: "easy",
			Tags: []string{"Python", "条件判断"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 3,
			Statement:        "## 三个数的最大值\n\n读入三个整数，输出其中的最大值。可以比较数值大小，不需要排序。",
			InputDescription: "一行三个整数 a、b、c。", OutputDescription: "一个整数，表示最大值。",
			Samples: []catalog.Sample{
				{Input: "1 9 4\n", Output: "9\n", Explanation: ""},
				{Input: "-5 -2 -8\n", Output: "-2\n", Explanation: "负数同样可以比较大小。"},
			},
			InitialCode: "a, b, c = map(int, input().split())\n# 使用 if 或 max() 找出最大值\n",
			Source:      trainingSource,
		},
		{
			ID: "train-loop-04", Code: "T-LOOP-04", Name: "偶数累加", Difficulty: "easy",
			Tags: []string{"Python", "循环", "range"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 4,
			Statement:        "## 偶数累加\n\n读入正整数 n，计算 1 到 n（包含 n）之间所有偶数的和。",
			InputDescription: "一个正整数 n。", OutputDescription: "一个整数，表示偶数和。",
			Samples: []catalog.Sample{
				{Input: "10\n", Output: "30\n", Explanation: "2 + 4 + 6 + 8 + 10 = 30。"},
				{Input: "7\n", Output: "12\n", Explanation: "2 + 4 + 6 = 12。"},
			},
			InitialCode: "n = int(input())\ntotal = 0\n# 遍历并累加偶数\n",
			Source:      trainingSource,
		},
		{
			ID: "train-while-05", Code: "T-WHILE-05", Name: "数字反转", Difficulty: "medium",
			Tags: []string{"Python", "while", "数字"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 5,
			Statement:        "## 数字反转\n\n读入一个非负整数，输出它倒序后的数字。反转后不能保留前导零。",
			InputDescription: "一个非负整数 n。", OutputDescription: "倒序后的整数；0 仍然输出 0。",
			Samples: []catalog.Sample{
				{Input: "120\n", Output: "21\n", Explanation: "反转后是 021，需要去掉前导零。"},
				{Input: "7\n", Output: "7\n", Explanation: ""},
			},
			InitialCode: "n = int(input())\n# 用 while 逐位取出数字\n",
			Source:      trainingSource,
		},
		{
			ID: "train-list-06", Code: "T-LIST-06", Name: "列表中的正数之和", Difficulty: "easy",
			Tags: []string{"Python", "列表", "遍历"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 6,
			Statement:        "## 列表中的正数之和\n\n第一行读入整数 n，第二行读入 n 个整数。输出其中所有正数的和。",
			InputDescription: "第一行一个整数 n；第二行 n 个整数。", OutputDescription: "一个整数，表示正数之和。",
			Samples: []catalog.Sample{
				{Input: "5\n1 -2 3 4 -5\n", Output: "8\n", Explanation: "1 + 3 + 4 = 8。"},
				{Input: "3\n-1 -2 -3\n", Output: "0\n", Explanation: "没有正数时输出 0。"},
			},
			InitialCode: "n = int(input())\nvalues = list(map(int, input().split()))\n# 遍历列表并累加正数\n",
			Source:      trainingSource,
		},
		{
			ID: "train-func-07", Code: "T-FUNC-07", Name: "最大公约数", Difficulty: "medium",
			Tags: []string{"Python", "函数", "算法"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 7,
			Statement:        "## 最大公约数\n\n读入两个正整数，输出它们的最大公约数。建议把计算过程封装成函数。",
			InputDescription: "一行两个正整数 a、b。", OutputDescription: "一个整数，表示最大公约数。",
			Samples: []catalog.Sample{
				{Input: "12 18\n", Output: "6\n", Explanation: ""},
				{Input: "17 5\n", Output: "1\n", Explanation: "两个数互质。"},
			},
			InitialCode: "a, b = map(int, input().split())\n# 实现欧几里得算法\n",
			Source:      trainingSource,
		},
		{
			ID: "train-dict-08", Code: "T-DICT-08", Name: "出现最多的字符", Difficulty: "medium",
			Tags: []string{"Python", "字典", "计数"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 8,
			Statement:        "## 出现最多的字符\n\n读入一行由小写字母组成的字符串（不含空格）。输出出现次数最多的字符和次数，中间用一个空格分隔。如果多个字符出现次数相同，输出字典序最小的字符。",
			InputDescription: "一行小写字母字符串。", OutputDescription: "字符和出现次数，中间一个空格。",
			Samples: []catalog.Sample{
				{Input: "banana\n", Output: "a 3\n", Explanation: "a 出现 3 次。"},
				{Input: "abcabc\n", Output: "a 2\n", Explanation: "a、b、c 都出现 2 次，取字典序最小的 a。"},
			},
			InitialCode: "text = input().strip()\n# 用字典统计每个字符出现的次数\n",
			Source:      trainingSource,
		},
		{
			ID: "train-str-09", Code: "T-STR-09", Name: "回文字符串判断", Difficulty: "easy",
			Tags: []string{"Python", "字符串"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 9,
			Statement:        "## 回文字符串判断\n\n读入一个由小写字母组成的字符串，判断它从左向右和从右向左读是否相同。相同输出 YES，否则输出 NO。",
			InputDescription: "一行小写字母字符串。", OutputDescription: "YES 或 NO。",
			Samples: []catalog.Sample{
				{Input: "level\n", Output: "YES\n", Explanation: ""},
				{Input: "agent\n", Output: "NO\n", Explanation: ""},
			},
			InitialCode: "text = input().strip()\n# 比较字符串和它的反转\n",
			Source:      trainingSource,
		},
		{
			ID: "train-bin-10", Code: "T-BIN-10", Name: "二分查找位置", Difficulty: "medium",
			Tags: []string{"Python", "查找", "算法"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 10,
			Statement:        "## 二分查找位置\n\n第一行读入 n，第二行读入 n 个严格递增的整数，第三行读入目标值 target。输出 target 在数组中的下标（从 0 开始）；不存在时输出 -1。",
			InputDescription: "第一行 n；第二行 n 个递增整数；第三行 target。", OutputDescription: "目标下标或 -1。",
			Samples: []catalog.Sample{
				{Input: "5\n1 3 5 7 9\n7\n", Output: "3\n", Explanation: "7 位于下标 3。"},
				{Input: "4\n2 4 6 8\n5\n", Output: "-1\n", Explanation: "5 不在数组中。"},
			},
			InitialCode: "n = int(input())\nvalues = list(map(int, input().split()))\ntarget = int(input())\n# 使用二分查找\n",
			Source:      trainingSource,
		},
		{
			ID: "train-ml-11", Code: "T-ML-11", Name: "最近邻分类", Difficulty: "medium",
			Tags: []string{"AI", "KNN", "距离"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 11,
			Statement:         "## 最近邻分类\n\n给定 n 个带标签的二维样本，再给定一个查询点。请输出距离查询点最近的样本标签。距离使用欧氏距离。如果有多个样本距离相同，输出字典序最小的标签。",
			InputDescription:  "第一行 n；接下来 n 行，每行两个整数 x、y 和一个大写字母标签；最后一行查询点 qx、qy。",
			OutputDescription: "一个标签字母。",
			Samples: []catalog.Sample{
				{Input: "3\n0 0 A\n3 4 B\n10 10 A\n2 2\n", Output: "B\n", Explanation: "点 (3,4) 距离约 2.24，是最近的样本。"},
				{Input: "2\n0 0 B\n3 4 A\n1 1\n", Output: "B\n", Explanation: "点 (0,0) 距离约 1.41，更近。"},
			},
			InitialCode: "n = int(input())\nsamples = []\n# 读取样本并计算最近距离\n",
			Source:      trainingSource,
		},
		{
			ID: "train-ml-12", Code: "T-ML-12", Name: "混淆矩阵指标", Difficulty: "medium",
			Tags: []string{"AI", "评估", "混淆矩阵"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 12,
			Statement:         "## 混淆矩阵指标\n\n读入三个整数 TP、FP、FN，分别表示真正例、假正例和假反例的数量。依次输出精确率 precision、召回率 recall 和 F1 分数，每个结果保留两位小数。\n\nprecision = TP / (TP + FP)\n\nrecall = TP / (TP + FN)\n\nF1 = 2 × precision × recall / (precision + recall)",
			InputDescription:  "一行三个非负整数 TP、FP、FN，且 TP 大于 0。",
			OutputDescription: "三行，分别为 precision、recall、F1，保留两位小数。",
			Samples: []catalog.Sample{
				{Input: "8 2 2\n", Output: "0.80\n0.80\n0.80\n", Explanation: ""},
				{Input: "5 5 1\n", Output: "0.50\n0.83\n0.63\n", Explanation: "保留两位小数时使用四舍五入。"},
			},
			InitialCode: "tp, fp, fn = map(int, input().split())\n# 计算并格式化三个指标\n",
			Source:      trainingSource,
		},
		{
			ID: "train-html-13", Code: "T-HTML-13", Name: "个人名片页", Difficulty: "easy",
			Language: "html", Tags: []string{"HTML", "CSS", "页面"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 13,
			Statement:        "## 个人名片页\n\n使用 HTML 和 CSS 完成一个居中的个人名片。页面需要包含姓名、身份、一段自我介绍和一个技能列表。\n\n要求：\n\n- 使用 index.html 组织结构，使用 styles.css 设置样式；\n- 名片在页面中水平垂直居中；\n- 页面在窄屏下仍然可读，不出现横向滚动。\n\n本题使用工作区的隔离预览，不进入 stdin/stdout 评测。",
			InputDescription: "无标准输入。", OutputDescription: "在隔离预览中显示居中的个人名片。",
			InitialCode: "",
			Files: []File{
				{Path: "index.html", Content: `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>个人名片</title>
    <link rel="stylesheet" href="styles.css" />
  </head>
  <body>
    <main class="card">
      <h1>你的名字</h1>
      <p class="role">编程学习者</p>
      <p>把这里改成你的自我介绍，并让名片在页面中居中显示。</p>
      <ul>
        <li>Python</li>
        <li>HTML / CSS</li>
      </ul>
    </main>
  </body>
</html>
`},
				{Path: "styles.css", Content: `:root {
  color-scheme: light;
}
* {
  box-sizing: border-box;
}
body {
  margin: 0;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 16px;
  background: #faf4e8;
  color: #2c3d42;
  font-family: system-ui, sans-serif;
}
.card {
  width: min(420px, 100%);
  padding: 32px;
  border: 1px solid #ecdfca;
  border-radius: 22px;
  background: #fffdf8;
  box-shadow: 0 12px 28px rgb(120 91 46 / 8%);
}
h1 {
  margin: 0 0 8px;
  color: #1e7d6b;
}
.role {
  margin: 0 0 16px;
  color: #a8482b;
  font-weight: 700;
}
ul {
  margin: 0;
  padding-left: 20px;
}
`},
			},
			Source: trainingSource,
		},
		{
			ID: "train-html-14", Code: "T-HTML-14", Name: "待办清单", Difficulty: "medium",
			Language: "html", Tags: []string{"HTML", "JavaScript", "DOM"}, TimeLimitMS: 1000, MemoryLimitMB: 128, Order: 14,
			Statement:        "## 待办清单\n\n完成一个简单的待办清单页面：输入内容后点击“添加”，清单中新增一项；点击某一项的“完成”按钮，可以切换完成状态。\n\n要求：\n\n- 使用 index.html、styles.css 和 script.js 三个文件；\n- JavaScript 写在 script.js 中，通过 DOM 事件监听处理添加和完成操作；\n- 空输入不添加条目；添加后清空输入框。\n\n本题使用工作区的隔离预览，不进入 stdin/stdout 评测。",
			InputDescription: "无标准输入。", OutputDescription: "在隔离预览中操作待办清单。",
			InitialCode: "",
			Files: []File{
				{Path: "index.html", Content: `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>待办清单</title>
    <link rel="stylesheet" href="styles.css" />
    <script src="script.js" defer></script>
  </head>
  <body>
    <main class="todo">
      <h1>今日待办</h1>
      <form id="todo-form">
        <input id="todo-input" type="text" placeholder="写下一件要做的事" maxlength="80" />
        <button type="submit">添加</button>
      </form>
      <ul id="todo-list"></ul>
    </main>
  </body>
</html>
`},
				{Path: "styles.css", Content: `:root {
  color-scheme: light;
}
* {
  box-sizing: border-box;
}
body {
  margin: 0;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 16px;
  background: #faf4e8;
  color: #2c3d42;
  font-family: system-ui, sans-serif;
}
.todo {
  width: min(460px, 100%);
  padding: 28px;
  border: 1px solid #ecdfca;
  border-radius: 22px;
  background: #fffdf8;
  box-shadow: 0 12px 28px rgb(120 91 46 / 8%);
}
h1 {
  margin: 0 0 16px;
  color: #1e7d6b;
}
form {
  display: flex;
  gap: 8px;
}
input {
  min-width: 0;
  flex: 1;
  padding: 10px 12px;
  border: 1px solid #96866f;
  border-radius: 10px;
  font: inherit;
}
button {
  padding: 10px 14px;
  border: 0;
  border-radius: 10px;
  background: #1e7d6b;
  color: #fff;
  font: inherit;
  font-weight: 700;
}
ul {
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
}
li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid #ecdfca;
}
li.done span {
  color: #5b6b65;
  text-decoration: line-through;
}
`},
				{Path: "script.js", Content: `const form = document.querySelector('#todo-form');
const input = document.querySelector('#todo-input');
const list = document.querySelector('#todo-list');

form.addEventListener('submit', (event) => {
  event.preventDefault();
  const text = input.value.trim();
  if (!text) return;
  const item = document.createElement('li');
  const label = document.createElement('span');
  label.textContent = text;
  const done = document.createElement('button');
  done.type = 'button';
  done.textContent = '完成';
  done.addEventListener('click', () => item.classList.toggle('done'));
  item.append(label, done);
  list.append(item);
  input.value = '';
  input.focus();
});
`},
			},
			Source: trainingSource,
		},
	}
	for i := range problems {
		if problems[i].Language == "" {
			problems[i].Language = "python"
		}
	}
	return problems
}

func demoCourses(classID string) []Course {
	return []Course{
		{
			ID: "course-python-basics", Code: "COURSE-PY-01", Name: "Python 编程基础",
			Description: "从输出、变量、条件、循环到函数和查找，按小节逐步完成 Python 编程入门。",
			ClassIDs:    []string{classID}, Order: 1, Source: trainingSource,
			Sections: []Section{
				{
					ID: "print-variables", Title: "输出、变量与算术", ProblemIDs: []string{"train-print-01", "train-arith-02"},
					Document: "## 输出、变量与算术\n\nPython 使用 print() 把结果写到标准输出。字符串需要放在引号中，整数和浮点数可以直接参与算术运算。\n\n~~~python\nname = \"AgentLab\"\nprint(\"Hello,\", name)\nprint(3 + 5)\n~~~\n\n读入一行两个整数时，可以使用 input().split() 和 map(int, ...) 转换类型。\n\n### 本节练习\n\n先完成“输出第一行代码”，再完成“两个整数的和与差”。注意输出中的空格、换行和大小写。",
				},
				{
					ID: "conditions", Title: "条件判断", ProblemIDs: []string{"train-cond-03"},
					Document: "## 条件判断\n\nif、elif 和 else 根据条件选择不同的执行路径。比较运算返回布尔值 True 或 False。\n\n~~~python\nif a >= b and a >= c:\n    print(a)\nelse:\n    print(\"继续比较\")\n~~~\n\n比较多个值时，要保证每个分支都有明确的处理，避免遗漏相等的情况。\n\n### 本节练习\n\n完成“三个数的最大值”，分别测试正数、负数和相等的情况。",
				},
				{
					ID: "loops", Title: "循环与累加", ProblemIDs: []string{"train-loop-04", "train-while-05"},
					Document: "## 循环与累加\n\nfor 循环适合遍历已知范围，range(n) 生成从 0 到 n-1 的整数。while 循环适合条件驱动的重复执行。\n\n~~~python\ntotal = 0\nfor value in range(1, n + 1):\n    if value % 2 == 0:\n        total += value\n~~~\n\n累加前先确定初始值。使用 while 处理数字时，记得更新循环变量，避免死循环。\n\n### 本节练习\n\n完成“偶数累加”和“数字反转”。反转数字时可以用取余和整除逐位处理。",
				},
				{
					ID: "collections", Title: "列表、字典与字符串", ProblemIDs: []string{"train-list-06", "train-dict-08", "train-str-09"},
					Document: "## 列表、字典与字符串\n\n列表保存有序元素，可以通过下标访问、修改或遍历。字典用键值对保存计数和映射。字符串支持切片和反转。\n\n~~~python\ncounts = {}\nfor char in text:\n    counts[char] = counts.get(char, 0) + 1\n~~~\n\n统计众数时，除了比较次数，还要处理并列情况。字符串反转可以使用 text[::-1]。\n\n### 本节练习\n\n完成“列表中的正数之和”“出现最多的字符”和“回文字符串判断”。",
				},
				{
					ID: "functions", Title: "函数与算法", ProblemIDs: []string{"train-func-07"},
					Document: "## 函数与算法\n\ndef 用来定义函数，return 把计算结果交还给调用者。把重复逻辑封装成函数，可以让代码更容易阅读和测试。\n\n~~~python\ndef gcd(a, b):\n    while b:\n        a, b = b, a % b\n    return a\n~~~\n\n欧几里得算法通过不断取余缩小问题规模，是理解循环和递归的好例子。\n\n### 本节练习\n\n完成“最大公约数”，并尝试自己写出两组合法输入进行验证。",
				},
				{
					ID: "search", Title: "二分查找", ProblemIDs: []string{"train-bin-10"},
					Document: "## 二分查找\n\n二分查找在一个有序数组中反复缩小搜索区间。每次比较中间元素后，根据大小关系排除一半区间。\n\n~~~python\nleft, right = 0, len(values) - 1\nwhile left <= right:\n    mid = (left + right) // 2\n    if values[mid] == target:\n        break\n~~~\n\n循环条件和区间更新必须保持一致，否则容易漏掉边界或陷入死循环。\n\n### 本节练习\n\n完成“二分查找位置”，并测试目标在开头、结尾和不存在的情况。",
				},
			},
		},
		{
			ID: "course-ai-foundations", Code: "COURSE-AI-01", Name: "AI 思维入门",
			Description: "从最近邻分类和混淆矩阵出发，理解分类、评估与模型训练的基本概念。",
			ClassIDs:    []string{classID}, Order: 2, Source: trainingSource,
			Sections: []Section{
				{
					ID: "nearest-neighbor", Title: "监督学习与最近邻", ProblemIDs: []string{"train-ml-11"},
					Document: "## 监督学习与最近邻\n\n监督学习使用带标签的数据学习输入到输出的映射。K 近邻是一种直观的分类方法：找到距离新样本最近的已知样本，用它们的标签进行投票。\n\n距离衡量方式会影响结果。二维平面常用欧氏距离：\n\n~~~text\ndistance = sqrt((x1 - x2)^2 + (y1 - y2)^2)\n~~~\n\n比较距离时可以只比较平方值，避免开方带来的精度问题。\n\n### 本节练习\n\n完成“最近邻分类”，思考当多个样本距离相同时应该如何稳定地选择结果。",
				},
				{
					ID: "metrics", Title: "分类评估与混淆矩阵", ProblemIDs: []string{"train-ml-12"},
					Document: "## 分类评估与混淆矩阵\n\n混淆矩阵把预测结果和真实标签组合成真正例、假正例、真反例和假反例。精确率关注预测为正的样本中有多少是真的，召回率关注真实正例中有多少被找到。\n\n精确率和召回率往往需要权衡。F1 分数是两者的调和平均，适合作为综合指标。\n\n### 本节练习\n\n完成“混淆矩阵指标”，观察 TP 不变时 FP 和 FN 如何影响三个指标。",
				},
				{
					ID: "overfitting", Title: "过拟合与数据划分",
					Document: "## 过拟合与数据划分\n\n模型在训练数据上表现很好，但在新数据上表现差，通常是过拟合。数据量有限时，可以把数据划分为训练集、验证集和测试集。\n\n验证集用于选择模型和调整参数，测试集用于最终评估。不能反复使用测试集调参，否则评估结果会过于乐观。\n\n### 思考\n\n如果同一份数据既用来训练又用来评估，结果为什么不可靠？",
				},
				{
					ID: "gradient-descent", Title: "梯度下降直观理解",
					Document: "## 梯度下降直观理解\n\n梯度下降通过沿着损失函数下降的方向调整参数。学习率太大会震荡甚至发散，太小则收敛缓慢。\n\n~~~text\n参数 = 参数 - 学习率 × 梯度\n~~~\n\n实际训练中还要关注数据缩放、损失函数和停止条件。理解梯度方向比记住公式更重要。",
				},
			},
		},
		{
			ID: "course-html-basics", Code: "COURSE-HTML-01", Name: "HTML 页面基础",
			Description: "从页面结构和样式开始，完成一个个人名片和一个可交互的待办清单。",
			ClassIDs:    []string{classID}, Order: 3, Source: trainingSource,
			Sections: []Section{
				{
					ID: "html-structure", Title: "HTML 结构与样式", ProblemIDs: []string{"train-html-13"},
					Document: "## HTML 结构与样式\n\nHTML 负责页面结构，CSS 负责颜色、间距和布局。一个基础页面通常包含 doctype、head、body 和语义化内容元素。\n\n~~~html\n<main class=\"card\">\n  <h1>标题</h1>\n  <p>说明文字</p>\n</main>\n~~~\n\n使用 Flexbox 或 Grid 可以轻松实现水平垂直居中。窄屏下要避免固定宽度撑出横向滚动。\n\n### 本节练习\n\n完成“个人名片页”，在工作区中打开隔离预览，观察样式修改后的即时变化。",
				},
				{
					ID: "dom-events", Title: "DOM 与事件", ProblemIDs: []string{"train-html-14"},
					Document: "## DOM 与事件\n\nJavaScript 可以通过 document.querySelector 找到页面元素，再用 addEventListener 监听用户操作。\n\n~~~javascript\nform.addEventListener('submit', (event) => {\n  event.preventDefault();\n  // 读取输入并更新页面\n});\n~~~\n\n动态创建元素时，先创建节点、设置内容，再 append 到页面。表单提交需要阻止默认刷新。\n\n### 本节练习\n\n完成“待办清单”，把脚本写在 script.js 中，并在隔离预览中测试添加和完成操作。",
				},
			},
		},
	}
}
