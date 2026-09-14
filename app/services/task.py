from sqlalchemy.orm import Session

from app import models, schemas


def create_task(session: Session, data: schemas.TaskCreate) -> models.SyncTask:
    task = models.SyncTask(**data.model_dump())
    session.add(task)
    session.commit()
    session.refresh(task)
    return task


def update_task(
    session: Session, task: models.SyncTask, data: schemas.TaskUpdate
) -> models.SyncTask:
    for key, value in data.model_dump(exclude_unset=True).items():
        setattr(task, key, value)
    session.commit()
    session.refresh(task)
    return task
