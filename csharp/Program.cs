// Program.cs - MediaSorterCN WinForms 界面(.NET Framework 4.0 / C# 5)
// 中文界面:源/目标文件夹、年/月/日结构、复制/移动、去重、时间偏移、日志、进度
using System;
using System.ComponentModel;
using System.Drawing;
using System.IO;
using System.Windows.Forms;

namespace MediaSorterCN
{
    static class Program
    {
        [STAThread]
        static void Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new MainForm());
        }
    }

    public class MainForm : Form
    {
        private TextBox _srcBox, _dstBox;
        private RadioButton _rYear, _rYearMonth, _rYearMonthDay;
        private CheckBox _chkMove, _chkDedupe;
        private NumericUpDown _offset;
        private Button _btnStart, _btnCancel;
        private TextBox _log;
        private ProgressBar _progress;
        private BackgroundWorker _worker;

        public MainForm()
        {
            Text = "MediaSorterCN - 按拍摄时间整理照片视频";
            Font = new Font("Microsoft YaHei UI", 9F);
            ClientSize = new Size(760, 560);
            MinimumSize = new Size(700, 480);
            StartPosition = FormStartPosition.CenterScreen;

            int y = 16, x = 16, labelW = 70, boxW = 560;

            // 源文件夹
            AddLabel(x, y + 3, "源文件夹:");
            _srcBox = new TextBox { Location = new Point(x + labelW, y), Width = boxW, Text = "" };
            Controls.Add(_srcBox);
            Button srcBtn = MakeButton("浏览", x + labelW + boxW + 6, y - 2, () => PickFolder(_srcBox));
            y += 34;

            // 目标文件夹
            AddLabel(x, y + 3, "目标文件夹:");
            _dstBox = new TextBox { Location = new Point(x + labelW, y), Width = boxW, Text = "" };
            Controls.Add(_dstBox);
            Button dstBtn = MakeButton("浏览", x + labelW + boxW + 6, y - 2, () => PickFolder(_dstBox));
            y += 40;

            // 目录结构
            AddLabel(x, y + 3, "目录结构:");
            _rYearMonth = new RadioButton { Text = "年/月", Checked = true, Location = new Point(x + labelW, y) };
            _rYear = new RadioButton { Text = "仅年", Location = new Point(x + labelW + 100, y) };
            _rYearMonthDay = new RadioButton { Text = "年/月/日", Location = new Point(x + labelW + 190, y) };
            Controls.AddRange(new Control[] { _rYearMonth, _rYear, _rYearMonthDay });
            y += 30;

            // 复制/移动 + 去重
            _chkMove = new CheckBox { Text = "移动文件(默认只复制,源文件不动)", Location = new Point(x + labelW, y) };
            _chkDedupe = new CheckBox { Text = "去重(相同内容只留一份)", Checked = true, Location = new Point(x + labelW + 260, y) };
            Controls.AddRange(new Control[] { _chkMove, _chkDedupe });
            y += 30;

            // 时间偏移
            AddLabel(x, y + 3, "时间偏移:");
            _offset = new NumericUpDown { Location = new Point(x + labelW, y), Width = 80, Minimum = -86400, Maximum = 86400, Value = 0 };
            Controls.Add(_offset);
            AddLabel(x + labelW + 88, y + 3, "秒(修正机内时间差,负=提前)");
            y += 40;

            // 开始/取消
            _btnStart = MakeButton("开始整理", x + labelW, y, StartRun);
            _btnStart.BackColor = Color.FromArgb(46, 139, 87);
            _btnStart.ForeColor = Color.White;
            _btnCancel = MakeButton("取消", x + labelW + 110, y, CancelRun);
            _btnCancel.Enabled = false;
            y += 42;

            // 进度条
            _progress = new ProgressBar { Location = new Point(x, y), Width = ClientSize.Width - 2 * x, Height = 14 };
            Controls.Add(_progress);
            y += 26;

            // 日志
            _log = new TextBox
            {
                Location = new Point(x, y),
                Size = new Size(ClientSize.Width - 2 * x, ClientSize.Height - y - 16),
                Multiline = true,
                ReadOnly = true,
                ScrollBars = ScrollBars.Vertical,
                BackColor = Color.FromArgb(30, 30, 34),
                ForeColor = Color.FromArgb(220, 225, 232),
                Font = new Font("Consolas", 9F)
            };
            Controls.Add(_log);

            // 后台任务
            _worker = new BackgroundWorker { WorkerReportsProgress = true, WorkerSupportsCancellation = true };
            _worker.DoWork += WorkerDoWork;
            _worker.ProgressChanged += (s, e) =>
            {
                if (e.UserState is string) AppendLog((string)e.UserState);
            };
            _worker.RunWorkerCompleted += (s, e) =>
            {
                _btnStart.Enabled = true;
                _btnCancel.Enabled = false;
                if (e.Error != null) AppendLog("发生错误: " + e.Error.Message);
                else AppendLog("整理完成!");
            };
        }

        private Button MakeButton(string text, int x, int y, Action onClick)
        {
            var b = new Button { Text = text, Location = new Point(x, y), Width = 100 };
            b.Click += (s, e) => onClick();
            Controls.Add(b);
            return b;
        }

        private void AddLabel(int x, int y, string text)
        {
            Controls.Add(new Label { Text = text, Location = new Point(x, y), AutoSize = true });
        }

        private void PickFolder(TextBox box)
        {
            using (var dlg = new FolderBrowserDialog())
            {
                if (dlg.ShowDialog(this) == DialogResult.OK)
                    box.Text = dlg.SelectedPath;
            }
        }

        private void AppendLog(string msg)
        {
            _log.AppendText(msg + Environment.NewLine);
        }

        private void StartRun()
        {
            string src = _srcBox.Text.Trim();
            string dst = _dstBox.Text.Trim();
            if (src.Length == 0 || dst.Length == 0)
            {
                MessageBox.Show("请先选择源文件夹和目标文件夹", "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            if (!Directory.Exists(src))
            {
                MessageBox.Show("源文件夹不存在: " + src, "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            if (!Directory.Exists(dst))
            {
                var r = MessageBox.Show("目标文件夹不存在,是否创建?" + Environment.NewLine + dst,
                    "提示", MessageBoxButtons.YesNo, MessageBoxIcon.Question);
                if (r != DialogResult.Yes) return;
                Directory.CreateDirectory(dst);
            }
            if (Path.GetFullPath(src).StartsWith(Path.GetFullPath(dst), StringComparison.OrdinalIgnoreCase) ||
                Path.GetFullPath(dst).StartsWith(Path.GetFullPath(src), StringComparison.OrdinalIgnoreCase))
            {
                MessageBox.Show("源和目标文件夹不能互相包含,否则会递归复制!", "警告",
                    MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }

            _btnStart.Enabled = false;
            _btnCancel.Enabled = true;
            _log.Clear();
            _progress.Style = ProgressBarStyle.Marquee;

            var args = new object[]
            {
                src, dst, _chkMove.Checked, _chkDedupe.Checked, (int)_offset.Value,
                _rYear.Checked ? FolderMode.Year : (_rYearMonthDay.Checked ? FolderMode.YearMonthDay : FolderMode.YearMonth)
            };
            _worker.RunWorkerAsync(args);
        }

        private void CancelRun()
        {
            _worker.CancelAsync();
            AppendLog("请求取消,等待当前文件处理完...");
        }

        private void WorkerDoWork(object sender, DoWorkEventArgs e)
        {
            var a = (object[])e.Argument;
            var scanner = new MediaScanner();
            scanner.Log += msg =>
            {
                ((BackgroundWorker)sender).ReportProgress(0, msg);
                if (((BackgroundWorker)sender).CancellationPending)
                    throw new OperationCanceledException();
            };
            var result = scanner.Run((string)a[0], (string)a[1], (bool)a[2], (bool)a[3], (int)a[4], (FolderMode)a[5]);
            scanner.Log += msg => ((BackgroundWorker)sender).ReportProgress(0, msg);
            ((BackgroundWorker)sender).ReportProgress(0, string.Format(
                "结果: 处理 {0} 个, 去重跳过 {1} 个, 失败 {2} 个", result.Item1, result.Item2, result.Item3));
        }
    }
}
