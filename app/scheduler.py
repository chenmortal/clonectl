import logging

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from app.models import CheckTask, RunTrigger, SyncTask
from app.rclone.client import RcloneClient
from app.services.check import poll_running_checks, run_check
from app.services.runner import poll_running_runs, run_task

logger = logging.getLogger(__name__)

POLL_JOB_ID = "poll-running-runs"
POLL_CHECK_JOB_ID = "poll-running-checks"


def job_id_for_task(task_id: int) -> str:
    return f"task-{task_id}"


def job_id_for_check_task(check_task_id: int) -> str:
    return f"checktask-{check_task_id}"


def parse_cron(expression: str) -> CronTrigger:
    fields = expression.split()
    if len(fields) != 5:
        raise ValueError(f"cron expression must have 5 fields: {expression!r}")
    minute, hour, day, month, day_of_week = fields
    return CronTrigger(
        minute=minute, hour=hour, day=day, month=month, day_of_week=day_of_week
    )


class SchedulerService:
    def __init__(
        self,
        session_factory: sessionmaker,
        client: RcloneClient,
        poll_interval_seconds: int = 10,
    ):
        self.session_factory = session_factory
        self.client = client
        self.poll_interval_seconds = poll_interval_seconds
        self.scheduler = BackgroundScheduler()
        self.is_leader = False

    def set_leader(self, value: bool) -> None:
        """Hook called by LeaderElector on acquired/lost transitions.

        On gain: re-sync enabled tasks from DB so we pick up any writes that
        landed while we were standby. On loss: pause user-scheduled jobs so
        no more rclone runs start on this node.
        """
        if value == self.is_leader:
            return
        was_leader = self.is_leader
        self.is_leader = value
        if value and not was_leader:
            self.sync_from_db()
            logger.info("scheduler: gained leadership; re-synced from db")
        elif not value and was_leader:
            self._pause_user_jobs()
            logger.info("scheduler: lost leadership; paused user jobs")

    def _execute_task(self, task_id: int) -> None:
        with self.session_factory() as session:
            run_task(session, self.client, task_id, RunTrigger.schedule)

    def _execute_check(self, check_task_id: int) -> None:
        with self.session_factory() as session:
            run_check(session, self.client, check_task_id, RunTrigger.schedule)

    def register_task(self, task: SyncTask) -> None:
        if not self.is_leader:
            return
        job_id = job_id_for_task(task.id)
        if self.scheduler.get_job(job_id):
            self.scheduler.reschedule_job(job_id, trigger=parse_cron(task.cron))
        else:
            self.scheduler.add_job(
                self._execute_task,
                trigger=parse_cron(task.cron),
                args=[task.id],
                id=job_id,
                replace_existing=True,
            )
        logger.info("registered task %s (%s)", task.id, task.cron)

    def remove_task(self, task_id: int) -> None:
        if not self.is_leader:
            return
        job_id = job_id_for_task(task_id)
        if self.scheduler.get_job(job_id):
            self.scheduler.remove_job(job_id)
            logger.info("removed job %s", job_id)

    def register_check_task(self, check_task: CheckTask) -> None:
        if not self.is_leader:
            return
        job_id = job_id_for_check_task(check_task.id)
        if not check_task.cron:
            if self.scheduler.get_job(job_id):
                self.scheduler.remove_job(job_id)
            return
        if self.scheduler.get_job(job_id):
            self.scheduler.reschedule_job(job_id, trigger=parse_cron(check_task.cron))
        else:
            self.scheduler.add_job(
                self._execute_check,
                trigger=parse_cron(check_task.cron),
                args=[check_task.id],
                id=job_id,
                replace_existing=True,
            )
        logger.info("registered check task %s (%s)", check_task.id, check_task.cron)

    def remove_check_task(self, check_task_id: int) -> None:
        if not self.is_leader:
            return
        job_id = job_id_for_check_task(check_task_id)
        if self.scheduler.get_job(job_id):
            self.scheduler.remove_job(job_id)
            logger.info("removed job %s", job_id)

    def _pause_user_jobs(self) -> None:
        """Pause task-* and checktask-* jobs without removing them.

        Lets a running rclone call finish naturally; future firings are skipped.
        """
        for job in list(self.scheduler.get_jobs()):
            if job.id.startswith("task-") or job.id.startswith("checktask-"):
                try:
                    self.scheduler.pause_job(job.id)
                except Exception:
                    logger.exception("failed to pause job %s", job.id)

    def sync_from_db(self) -> None:
        with self.session_factory() as session:
            tasks = session.scalars(select(SyncTask)).all()
            check_tasks = session.scalars(select(CheckTask)).all()
        enabled_ids = set()
        for task in tasks:
            if task.enabled:
                self.register_task(task)
                enabled_ids.add(job_id_for_task(task.id))
        for check_task in check_tasks:
            if check_task.enabled and check_task.cron:
                self.register_check_task(check_task)
                enabled_ids.add(job_id_for_check_task(check_task.id))
        for job in list(self.scheduler.get_jobs()):
            if (job.id.startswith("task-") or job.id.startswith("checktask-")) and (
                job.id not in enabled_ids
            ):
                self.scheduler.remove_job(job.id)

    def start(self) -> None:
        self.scheduler.add_job(
            poll_running_runs,
            trigger="interval",
            seconds=self.poll_interval_seconds,
            args=[self.session_factory, self.client],
            id=POLL_JOB_ID,
            replace_existing=True,
        )
        self.scheduler.add_job(
            poll_running_checks,
            trigger="interval",
            seconds=self.poll_interval_seconds,
            args=[self.session_factory, self.client],
            id=POLL_CHECK_JOB_ID,
            replace_existing=True,
        )
        self.sync_from_db()
        self.scheduler.start()

    def shutdown(self) -> None:
        if self.scheduler.running:
            self.scheduler.shutdown(wait=False)
